package aes

import "github.com/gogf/gf/v2/crypto/gaes"

const (
	key = "metled@2009iPev*#xA,/3gS"
)

func EncryptLicense(data []byte) ([]byte, error) {
	en, err := gaes.Encrypt(data, []byte(key))
	if err != nil {
		return nil, err
	}
	return en, nil
}
