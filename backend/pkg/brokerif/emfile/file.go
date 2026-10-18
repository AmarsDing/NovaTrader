// Package emfile encodes the verified subset of Eastmoney CSV file orders.
// trade does not call it. Production file I/O belongs to the Windows broker-gw.
package emfile

import (
	"bytes"
	"crypto/sha256"
	"encoding/csv"
	"encoding/hex"
	"errors"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"server/pkg/brokerif"
	"server/pkg/symbol"
)

var (
	ErrUnknown     = errors.New("emfile: uncertain publication; query original sid, do not resubmit")
	ErrConflict    = errors.New("emfile: sid already used by a different request")
	idPattern      = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:#-]{0,95}$`)
	accountPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`)
)

// WriteOrder publishes a complete, fsynced CSV by a no-overwrite hard link.
// Scanner reads new/changed CSV immediately: .fin means no more appends,
// NOT acknowledgement or a commit trigger. Staging is OUTSIDE dir.
// The persistent sibling spool must survive terminal file cleanup. Repeated
// sid never publishes again. now is retained only for source compatibility.
func WriteOrder(dir, accountID string, order brokerif.Order, _ time.Time) (string, error) {
	body, err := encodeOrder(accountID, order)
	if err != nil {
		return "", err
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	if strings.HasPrefix(abs, `\\`) {
		return "", errors.New("emfile: network directory forbidden")
	}
	for path := abs; ; path = filepath.Dir(path) {
		info, e := os.Lstat(path)
		if e != nil {
			return "", e
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return "", errors.New("emfile: linked directory forbidden")
		}
		if path == filepath.Dir(path) {
			break
		}
	}
	identity := sha256.Sum256([]byte(accountID + "\x00" + order.ClientID))
	key := hex.EncodeToString(identity[:])
	spool := abs + ".novatrader-emfile"
	if err := os.MkdirAll(spool, 0700); err != nil {
		return "", err
	}
	info, err := os.Lstat(spool)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", errors.New("emfile: invalid spool")
	}
	claim := filepath.Join(spool, key)
	path := filepath.Join(abs, key+".order.csv")
	if err := os.Mkdir(claim, 0700); err != nil {
		if !os.IsExist(err) {
			return "", err
		}
		claimInfo, claimErr := os.Lstat(claim)
		if claimErr != nil || !claimInfo.IsDir() || claimInfo.Mode()&os.ModeSymlink != 0 {
			return "", ErrUnknown
		}
		// An in-flight writer can still be flushing request.csv. Never interpret
		// its incomplete bytes as a different request and never take over.
		if _, readErr := os.Stat(filepath.Join(claim, "published")); readErr != nil {
			return "", ErrUnknown
		}
		existing, readErr := os.ReadFile(filepath.Join(claim, "request.csv"))
		if readErr != nil {
			return "", ErrUnknown
		}
		if !bytes.Equal(existing, body) {
			return "", ErrConflict
		}
		return path, nil
	}
	if err := syncExclusive(filepath.Join(claim, "request.csv"), body); err != nil {
		return "", ErrUnknown
	}
	if err := os.Link(filepath.Join(claim, "request.csv"), path); err != nil {
		return "", ErrUnknown
	}
	if err := syncExclusive(path+".fin", nil); err != nil {
		return "", ErrUnknown
	}
	if err := syncExclusive(filepath.Join(claim, "published"), nil); err != nil {
		return "", ErrUnknown
	}
	return path, nil
}

func syncExclusive(path string, body []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	if _, err := f.Write(body); err != nil {
		return err
	}
	return f.Sync()
}

func encodeOrder(accountID string, order brokerif.Order) ([]byte, error) {
	if !idPattern.MatchString(order.ClientID) || !accountPattern.MatchString(accountID) || order.Volume <= 0 || order.Volume > 1_000_000_000 ||
		math.IsNaN(order.Price) || math.IsInf(order.Price, 0) || order.Price <= 0 || order.Price > 1_000_000 || math.Abs(order.Price*100-math.Round(order.Price*100)) > 1e-6 {
		return nil, errors.New("emfile: invalid order")
	}
	sym, err := symbol.Parse(order.Symbol)
	if err != nil {
		return nil, err
	}
	if sym.Market == "BJ" {
		return nil, errors.New("emfile: BJ terminal mapping is not verified")
	}
	biz := "1"
	if order.Side == brokerif.Sell {
		biz = "2"
	} else if order.Side != brokerif.Buy {
		return nil, errors.New("emfile: invalid side")
	}
	var body bytes.Buffer
	w := csv.NewWriter(&body)
	w.UseCRLF = true
	_ = w.Write([]string{"sid", "account_id", "symbol", "volume", "order_type", "order_business(order_biz)", "price", "comment"})
	_ = w.Write([]string{order.ClientID, accountID, sym.EastMoney(), strconv.Itoa(order.Volume), "1", biz, strconv.FormatFloat(order.Price, 'f', 2, 64), ""})
	w.Flush()
	return body.Bytes(), w.Error()
}
