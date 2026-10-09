package utils

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/gogf/gf/v2/crypto/gmd5"
	"github.com/gogf/gf/v2/encoding/gcharset"
	"github.com/gogf/gf/v2/encoding/gjson"
	"github.com/gogf/gf/v2/encoding/gurl"
	"github.com/gogf/gf/v2/frame/g"
	"github.com/gogf/gf/v2/net/ghttp"
)

// EncryptPassword 密码加密
func EncryptPassword(password, salt string) string {
	return gmd5.MustEncryptString(gmd5.MustEncryptString(password) + gmd5.MustEncryptString(salt))
}

// GetDomain 获取当前请求接口域名
func GetDomain(ctx context.Context) string {
	r := g.RequestFromCtx(ctx)
	pathInfo, err := gurl.ParseURL(r.GetUrl(), -1)
	if err != nil {
		g.Log().Error(ctx, err)
		return ""
	}
	return fmt.Sprintf("%s://%s:%s/", pathInfo["scheme"], pathInfo["host"], pathInfo["port"])
}

// GetClientIp 获取客户端IP
func GetClientIp(ctx context.Context) string {
	// return g.RequestFromCtx(ctx).GetClientIp()
	return "111"
}

// GetUserAgent 获取user-agent
func GetUserAgent(ctx context.Context) string {
	return ghttp.RequestFromCtx(ctx).Header.Get("User-Agent")
}

// GetLocalIP 服务端ip
func GetLocalIP() (ip string, err error) {
	var addrs []net.Addr
	addrs, err = net.InterfaceAddrs()
	if err != nil {
		return
	}
	for _, addr := range addrs {
		ipAddr, ok := addr.(*net.IPNet)
		if !ok {
			continue
		}
		if ipAddr.IP.IsLoopback() {
			continue
		}
		if !ipAddr.IP.IsGlobalUnicast() {
			continue
		}
		return ipAddr.IP.String(), nil
	}
	return
}

// GetCityByIp 获取ip所属城市
func GetCityByIp(ip string) string {
	if ip == "" {
		return ""
	}
	if ip == "[::1]" || ip == "127.0.0.1" {
		return "内网IP"
	}
	url := "http://whois.pconline.com.cn/ipJson.jsp?json=true&ip=" + ip
	bytes := g.Client().GetBytes(context.TODO(), url)
	src := string(bytes)
	srcCharset := "GBK"
	tmp, _ := gcharset.ToUTF8(srcCharset, src)
	json, err := gjson.DecodeToJson(tmp)
	if err != nil {
		return ""
	}
	if json.Get("code").Int() == 0 {
		city := fmt.Sprintf("%s %s", json.Get("pro").String(), json.Get("city").String())
		return city
	} else {
		return ""
	}
}

// 写入文件
func WriteToFile(fileName string, content string) error {
	f, err := os.OpenFile(fileName, os.O_WRONLY|os.O_TRUNC|os.O_CREATE, 0644)
	if err != nil {
		return err
	}
	n, _ := f.Seek(0, os.SEEK_END)
	_, err = f.WriteAt([]byte(content), n)
	defer f.Close()
	return err
}

// 文件或文件夹是否存在
func FileIsExisted(filename string) bool {
	existed := true
	if _, err := os.Stat(filename); os.IsNotExist(err) {
		existed = false
	}
	return existed
}

// 解析路径获取文件名称及后缀
func ParseFilePath(pathStr string) (fileName string, fileType string) {
	fileNameWithSuffix := path.Base(pathStr)
	fileType = path.Ext(fileNameWithSuffix)
	fileName = strings.TrimSuffix(fileNameWithSuffix, fileType)
	return
}

// IsNotExistMkDir 检查文件夹是否存在
// 如果不存在则新建文件夹
func IsNotExistMkDir(src string) error {
	if exist := !FileIsExisted(src); exist == false {
		if err := MkDir(src); err != nil {
			return err
		}
	}

	return nil
}

// MkDir 新建文件夹
func MkDir(src string) error {
	err := os.MkdirAll(src, os.ModePerm)
	if err != nil {
		return err
	}

	return nil
}

// 获取文件后缀
func GetExt(fileName string) string {
	return path.Ext(fileName)
}

// GetType 获取文件类型
func GetType(p string) (result string, err error) {
	file, err := os.Open(p)
	if err != nil {
		g.Log().Error(context.TODO(), err)
		return
	}
	buff := make([]byte, 512)

	_, err = file.Read(buff)

	if err != nil {
		g.Log().Error(context.TODO(), err)
		return
	}
	filetype := http.DetectContentType(buff)
	return filetype, nil
}

// 返回UTC 现在的时间格式
func UtcTimeStr() string {
	return time.Now().UTC().Format("2006-01-02 15:04:05")
}

// 返回UTC 311600  月时分格式
func UtcOBSTime() string {
	day := time.Now().UTC().Day()
	hour := time.Now().UTC().Hour()
	minute := time.Now().UTC().Minute()
	return fmt.Sprintf("%02d%02d%02d", day, hour, minute)
}

// 将字符串中的明文十六进制转化为字符串 <0x01> to string
func GetAscIIString(str string) string {
	if str == "" {
		return ""
	}
	if !strings.Contains(str, "<") {
		return str
	}

	newstring := str
	oldstring := str
	start := false
	index := 0
	headstr := ""
	endstr := ""

	for i, v := range str {
		if v == '<' {
			if start {
				index = i
			} else {
				start = true
				index = i
			}
		} else if v == '>' {
			if start {
				start = false
				headstr = oldstring[:index]
				endstr = oldstring[i+1:]
				tempstr := oldstring[index : i+1]
				var tempbyte byte
				switch strings.ToUpper(tempstr) {
				case "<NUL>":
					tempbyte = 0x00
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0X00>":
					tempbyte = 0x00
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<SOH>":
					tempbyte = 0x01
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0X01>":
					tempbyte = 0x01
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<STX>":
					tempbyte = 0x02
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0X02>":
					tempbyte = 0x02
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<ETX>":
					tempbyte = 0x03
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0X03>":
					tempbyte = 0x03
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<EOT>":
					tempbyte = 0x04
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0X04>":
					tempbyte = 0x04
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<ENQ>":
					tempbyte = 0x05
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0X05>":
					tempbyte = 0x05
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<ACK>":
					tempbyte = 0x06
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0X06>":
					tempbyte = 0x06
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<BEL>":
					tempbyte = 0x07
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0X07>":
					tempbyte = 0x07
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<BS>":
					tempbyte = 0x08
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0X08>":
					tempbyte = 0x08
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<TAB>":
					tempbyte = 0x09
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0X09>":
					tempbyte = 0x09
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<HT>":
					tempbyte = 0x09
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<LF>":
					tempbyte = 0x0a
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0X0A>":
					tempbyte = 0x0a
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<VT>":
					tempbyte = 0x0b
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0X0B>":
					tempbyte = 0x0b
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<FF>":
					tempbyte = 0x0c
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0X0C>":
					tempbyte = 0x0c
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<CR>":
					tempbyte = 0x0d
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0X0D>":
					tempbyte = 0x0d
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<SO>":
					tempbyte = 0x0e
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0X0E>":
					tempbyte = 0x0e
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<SI>":
					tempbyte = 0x0f
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0X0F>":
					tempbyte = 0x0f
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<DLE>":
					tempbyte = 0x10
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0X10>":
					tempbyte = 0x10
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<DC1>":
					tempbyte = 0x11
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0X11>":
					tempbyte = 0x11
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<DC2>":
					tempbyte = 0x12
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0X12>":
					tempbyte = 0x12
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<DC3>":
					tempbyte = 0x13
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0X13>":
					tempbyte = 0x13
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<DC4>":
					tempbyte = 0x14
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0X14>":
					tempbyte = 0x14
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<NAK>":
					tempbyte = 0x15
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0X15>":
					tempbyte = 0x15
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<SYN>":
					tempbyte = 0x16
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0X16>":
					tempbyte = 0x16
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<ETB>":
					tempbyte = 0x17
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0X17>":
					tempbyte = 0x17
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<CAN>":
					tempbyte = 0x18
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0X18>":
					tempbyte = 0x18
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<EM>":
					tempbyte = 0x19
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0X19>":
					tempbyte = 0x19
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<SUB>":
					tempbyte = 0x1a
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0X1A>":
					tempbyte = 0x1a
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<ESC>":
					tempbyte = 0x1b
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0X1B>":
					tempbyte = 0x1b
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<FS>":
					tempbyte = 0x1c
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0X1C>":
					tempbyte = 0x1c
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<GS>":
					tempbyte = 0x1d
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0X1D>":
					tempbyte = 0x1d
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<RS>":
					tempbyte = 0x1e
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0X1E>":
					tempbyte = 0x1e
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<US>":
					tempbyte = 0x1f
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0X1F>":
					tempbyte = 0x1f
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<SP>":
					tempbyte = 0x20
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0X20>":
					tempbyte = 0x20
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<SPACE>":
					tempbyte = 0x20
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0X21>":
					tempbyte = 0x21
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0X22>":
					tempbyte = 0x22
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0X23>":
					tempbyte = 0x23
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0X24>":
					tempbyte = 0x24
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0X25>":
					tempbyte = 0x25
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0X26>":
					tempbyte = 0x26
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0X27>":
					tempbyte = 0x27
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0X28>":
					tempbyte = 0x28
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0X29>":
					tempbyte = 0x29
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0X2A>":
					tempbyte = 0x2a
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0X2B>":
					tempbyte = 0x2b
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0X2C>":
					tempbyte = 0x2c
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0X2D>":
					tempbyte = 0x2d
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0X2E>":
					tempbyte = 0x2e
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0X2F>":
					tempbyte = 0x2f
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0X30>":
					tempbyte = 0x30
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0X31>":
					tempbyte = 0x31
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0X32>":
					tempbyte = 0x32
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0X33>":
					tempbyte = 0x33
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0X34>":
					tempbyte = 0x34
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0X35>":
					tempbyte = 0x35
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0X36>":
					tempbyte = 0x36
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0X37>":
					tempbyte = 0x37
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0X38>":
					tempbyte = 0x38
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0X39>":
					tempbyte = 0x39
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0X3A>":
					tempbyte = 0x3a
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0X3B>":
					tempbyte = 0x3b
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0X3C>":
					tempbyte = 0x3c
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0X3D>":
					tempbyte = 0x3d
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0X3E>":
					tempbyte = 0x3e
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0X3F>":
					tempbyte = 0x3f
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0X40>":
					tempbyte = 0x40
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0X41>":
					tempbyte = 0x41
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0X42>":
					tempbyte = 0x42
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0X43>":
					tempbyte = 0x43
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0X44>":
					tempbyte = 0x44
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0X45>":
					tempbyte = 0x45
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0X46>":
					tempbyte = 0x46
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0X47>":
					tempbyte = 0x47
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0X48>":
					tempbyte = 0x48
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0X49>":
					tempbyte = 0x49
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0X4A>":
					tempbyte = 0x4a
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0X4B>":
					tempbyte = 0x4b
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0X4C>":
					tempbyte = 0x4c
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0X4D>":
					tempbyte = 0x4d
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0X4E>":
					tempbyte = 0x4e
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0X4F>":
					tempbyte = 0x4f
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0X50>":
					tempbyte = 0x50
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0X51>":
					tempbyte = 0x51
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0X52>":
					tempbyte = 0x52
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0X53>":
					tempbyte = 0x53
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0X54>":
					tempbyte = 0x54
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0X55>":
					tempbyte = 0x55
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0X56>":
					tempbyte = 0x56
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0X57>":
					tempbyte = 0x57
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0X58>":
					tempbyte = 0x58
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0X59>":
					tempbyte = 0x59
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0X5A>":
					tempbyte = 0x5a
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0X5B>":
					tempbyte = 0x5b
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0X5C>":
					tempbyte = 0x5c
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0X5D>":
					tempbyte = 0x5d
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0X5E>":
					tempbyte = 0x5e
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0X5F>":
					tempbyte = 0x5f
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0X60>":
					tempbyte = 0x60
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0X61>":
					tempbyte = 0x61
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0X62>":
					tempbyte = 0x62
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0X63>":
					tempbyte = 0x63
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0X64>":
					tempbyte = 0x64
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0X65>":
					tempbyte = 0x65
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0X66>":
					tempbyte = 0x66
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0X67>":
					tempbyte = 0x67
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0X68>":
					tempbyte = 0x68
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0X69>":
					tempbyte = 0x69
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0X6A>":
					tempbyte = 0x6a
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0X6B>":
					tempbyte = 0x6b
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0X6C>":
					tempbyte = 0x6c
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0X6D>":
					tempbyte = 0x6d
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0X6E>":
					tempbyte = 0x6e
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0X6F>":
					tempbyte = 0x6f
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0X70>":
					tempbyte = 0x70
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0X71>":
					tempbyte = 0x71
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0X72>":
					tempbyte = 0x72
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0X73>":
					tempbyte = 0x73
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0X74>":
					tempbyte = 0x74
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0X75>":
					tempbyte = 0x75
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0X76>":
					tempbyte = 0x76
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0X77>":
					tempbyte = 0x77
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0X78>":
					tempbyte = 0x78
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0X79>":
					tempbyte = 0x79
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0X7A>":
					tempbyte = 0x7a
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0X7B>":
					tempbyte = 0x7b
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0X7C>":
					tempbyte = 0x7c
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0X7D>":
					tempbyte = 0x7d
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0X7E>":
					tempbyte = 0x7e
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<DEL>":
					tempbyte = 0x7f
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0X7F>":
					tempbyte = 0x7f
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0X80>":
					tempbyte = 0x80
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0X81>":
					tempbyte = 0x81
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0X82>":
					tempbyte = 0x82
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0X83>":
					tempbyte = 0x83
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0X84>":
					tempbyte = 0x84
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0X85>":
					tempbyte = 0x85
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0X86>":
					tempbyte = 0x86
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0X87>":
					tempbyte = 0x87
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0X88>":
					tempbyte = 0x88
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0X89>":
					tempbyte = 0x89
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0X8A>":
					tempbyte = 0x8a
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0X8B>":
					tempbyte = 0x8b
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0X8C>":
					tempbyte = 0x8c
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0X8D>":
					tempbyte = 0x8d
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0X8E>":
					tempbyte = 0x8e
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0X8F>":
					tempbyte = 0x8f
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0X90>":
					tempbyte = 0x90
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0X91>":
					tempbyte = 0x91
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0X92>":
					tempbyte = 0x92
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0X93>":
					tempbyte = 0x93
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0X94>":
					tempbyte = 0x94
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0X95>":
					tempbyte = 0x95
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0X96>":
					tempbyte = 0x96
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0X97>":
					tempbyte = 0x97
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0X98>":
					tempbyte = 0x98
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0X99>":
					tempbyte = 0x99
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0X9A>":
					tempbyte = 0x9a
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0X9B>":
					tempbyte = 0x9b
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0X9C>":
					tempbyte = 0x9c
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0X9D>":
					tempbyte = 0x9d
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0X9E>":
					tempbyte = 0x9e
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0X9F>":
					tempbyte = 0x9f
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0XA0>":
					tempbyte = 0xa0
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0XA1>":
					tempbyte = 0xa1
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0XA2>":
					tempbyte = 0xa2
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0XA3>":
					tempbyte = 0xa3
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0XA4>":
					tempbyte = 0xa4
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0XA5>":
					tempbyte = 0xa5
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0XA6>":
					tempbyte = 0xa6
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0XA7>":
					tempbyte = 0xa7
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0XA8>":
					tempbyte = 0xa8
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0XA9>":
					tempbyte = 0xa9
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0XAA>":
					tempbyte = 0xaa
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0XAB>":
					tempbyte = 0xab
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0XAC>":
					tempbyte = 0xac
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0XAD>":
					tempbyte = 0xad
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0XAE>":
					tempbyte = 0xae
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0XAF>":
					tempbyte = 0xaf
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0XB0>":
					tempbyte = 0xb0
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0XB1>":
					tempbyte = 0xb1
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0XB2>":
					tempbyte = 0xb2
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0XB3>":
					tempbyte = 0xb3
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0XB4>":
					tempbyte = 0xb4
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0XB5>":
					tempbyte = 0xb5
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0XB6>":
					tempbyte = 0xb6
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0XB7>":
					tempbyte = 0xb7
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0XB8>":
					tempbyte = 0xb8
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0XB9>":
					tempbyte = 0xb9
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0XBA>":
					tempbyte = 0xba
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0XBB>":
					tempbyte = 0xbb
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0XBC>":
					tempbyte = 0xbc
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0XBD>":
					tempbyte = 0xbd
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0XBE>":
					tempbyte = 0xbe
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0XBF>":
					tempbyte = 0xbf
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0XC0>":
					tempbyte = 0xc0
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0XC1>":
					tempbyte = 0xc1
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0XC2>":
					tempbyte = 0xc2
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0XC3>":
					tempbyte = 0xc3
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0XC4>":
					tempbyte = 0xc4
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0XC5>":
					tempbyte = 0xc5
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0XC6>":
					tempbyte = 0xc6
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0XC7>":
					tempbyte = 0xc7
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0XC8>":
					tempbyte = 0xc8
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0XC9>":
					tempbyte = 0xc9
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0XCA>":
					tempbyte = 0xca
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0XCB>":
					tempbyte = 0xcb
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0XCC>":
					tempbyte = 0xcc
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0XCD>":
					tempbyte = 0xcd
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0XCE>":
					tempbyte = 0xce
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0XCF>":
					tempbyte = 0xcf
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0XD0>":
					tempbyte = 0xd0
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0XD1>":
					tempbyte = 0xd1
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0XD2>":
					tempbyte = 0xd2
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0XD3>":
					tempbyte = 0xd3
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0XD4>":
					tempbyte = 0xd4
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0XD5>":
					tempbyte = 0xd5
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0XD6>":
					tempbyte = 0xd6
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0XD7>":
					tempbyte = 0xd7
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0XD8>":
					tempbyte = 0xd8
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0XD9>":
					tempbyte = 0xd9
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0XDA>":
					tempbyte = 0xda
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0XDB>":
					tempbyte = 0xdb
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0XDC>":
					tempbyte = 0xdc
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0XDD>":
					tempbyte = 0xdd
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0XDE>":
					tempbyte = 0xde
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0XDF>":
					tempbyte = 0xdf
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0XE0>":
					tempbyte = 0xe0
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0XE1>":
					tempbyte = 0xe1
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0XE2>":
					tempbyte = 0xe2
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0XE3>":
					tempbyte = 0xe3
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0XE4>":
					tempbyte = 0xe4
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0XE5>":
					tempbyte = 0xe5
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0XE6>":
					tempbyte = 0xe6
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0XE7>":
					tempbyte = 0xe7
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0XE8>":
					tempbyte = 0xe8
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0XE9>":
					tempbyte = 0xe9
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0XEA>":
					tempbyte = 0xea
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0XEB>":
					tempbyte = 0xeb
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0XEC>":
					tempbyte = 0xec
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0XED>":
					tempbyte = 0xed
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0XEE>":
					tempbyte = 0xee
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0XEF>":
					tempbyte = 0xef
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0XF0>":
					tempbyte = 0xf0
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0XF1>":
					tempbyte = 0xf1
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0XF2>":
					tempbyte = 0xf2
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0XF3>":
					tempbyte = 0xf3
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0XF4>":
					tempbyte = 0xf4
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0XF5>":
					tempbyte = 0xf5
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0XF6>":
					tempbyte = 0xf6
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0XF7>":
					tempbyte = 0xf7
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0XF8>":
					tempbyte = 0xf8
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0XF9>":
					tempbyte = 0xf9
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0XFA>":
					tempbyte = 0xfa
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0XFB>":
					tempbyte = 0xfb
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0XFC>":
					tempbyte = 0xfc
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0XFD>":
					tempbyte = 0xfd
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0XFE>":
					tempbyte = 0xfe
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				case "<0XFF>":
					tempbyte = 0xff
					newstring = strings.Replace(newstring, tempstr, string(tempbyte), 1)
				default:
					newstring = headstr + tempstr + endstr
				}
			}
		}
	}
	return newstring
}

// 十位四舍五入 126 = 130 124=120
func DecRound(val int) int {
	if (val % 10) >= 5 {
		val = val - val%10 + 10
	} else {
		val = val - val%10
	}
	return val
}

// 小数点后一位  四舍五入  1.5 = 2   1.4=1
func OnePointRound(val float64) int {
	if val > 0 {
		temp := int(val * 10)
		if temp%10 >= 5 {
			return int(val) + 1
		}
		return int(val)
	}
	temp := int(val * 10)
	if temp%10 < -5 {
		return int(val) - 1
	}
	return int(val)
}

// 小数点后两位  四舍五入  1.45 = 1.5   1.54=1.5  1.44=1.4  -1.45 = -1.4 -1.56=-1.6
func TwoPointRound(val float64) float64 {
	if val > 0 {
		temp := int(val * 100)
		if temp%10 >= 5 {
			temp = int(val*10) + 1
		} else {
			temp = int(val * 10)
		}
		return float64(temp) / 10
	}
	temp := int(val * 100)
	if temp%10 < -5 {
		temp = int(val*10) - 1
	} else {
		temp = int(val * 10)
	}
	return float64(temp) / 10
}

// 小数点后两位  1.45 = 2   1.54=2  1.44=1  -1.55 = -1 -1.56=-2
func TwoPointRound2(val float64) int {
	temp := TwoPointRound(val)
	return OnePointRound(temp)
}

// 将时间戳转化为时间字符串 “2006-01-02 15:04:05”
func TimeStampToString(timestamp string) string {
	timestampInt, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil {
		return ""
	}
	t := time.Unix(timestampInt, 0)
	return t.Format("2006-01-02 15:04:05")
}

// 将时间字符串转化为时间戳
func StringToTimeStamp(timeStr string) (int64, error) {
	t, err := time.Parse("2006-01-02 15:04:05", timeStr)
	if err != nil {
		return 0, err
	}
	return t.Unix(), nil
}
