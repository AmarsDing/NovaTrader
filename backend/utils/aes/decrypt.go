package aes

import "github.com/gogf/gf/v2/crypto/gaes"

func DecryptLicense(data []byte) ([]byte, error) {
	de, err := gaes.Decrypt(data, []byte(key))
	if err != nil {
		return nil, err
	}
	return de, nil
}
