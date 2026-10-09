// Package emfile 按东方财富量化终端文件单格式写委托 CSV。
// trade 服务不调用它。只有 Windows 上的 broker-gw 在实盘联调时写本地目录。
package emfile

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"server/pkg/brokerif"
	"server/pkg/symbol"
)

// WriteOrder 写入委托 CSV 和同名 `.fin`。终端看到 .fin 才开始读。
// 文档里的时间用冒号。Windows 文件名不能含冒号，这里写成 09-41-03。
// accountID 来自终端配置，不写进仓库。
func WriteOrder(dir, accountID string, order brokerif.Order, now time.Time) (string, error) {
	if order.ClientID == "" || order.Volume <= 0 || order.Price <= 0 {
		return "", fmt.Errorf("emfile: invalid order")
	}
	sym, err := symbol.Parse(order.Symbol)
	if err != nil {
		return "", err
	}
	biz := "1"
	if order.Side == brokerif.Sell {
		biz = "2"
	} else if order.Side != brokerif.Buy {
		return "", fmt.Errorf("emfile: side %s", order.Side)
	}
	name := now.Format("2006-01-02_15-04-05.000") + ".order.csv"
	path := filepath.Join(dir, name)
	body := "sid,account_id,symbol,volume,order_type,order_business(order_biz),price,comment\r\n" +
		fmt.Sprintf("%s,%s,%s,%d,1,%s,%.2f,\r\n", order.ClientID, accountID, sym.EastMoney(), order.Volume, biz, order.Price)
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		return "", err
	}
	if err := os.WriteFile(path+".fin", nil, 0o644); err != nil {
		return "", err
	}
	return path, nil
}
