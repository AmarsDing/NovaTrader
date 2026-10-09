package utils

import (
	"fmt"
	v1 "server/api/common/v1"
	"strconv"
	"strings"
)

const (
	soh           = string(0x01)
	stx           = string(0x02)
	etx           = string(0x03)
	eot           = string(0x04)
	sep           = "|"
	TIME_FORMAT   = "2006-01-02 15:04:05"
	ALARM_OK      = 0
	ALARM_WARNIG  = 1
	ALARM_ERROR   = 2
	ALARM_DISCONN = 3
)

func HB2Protoc(data string) ([]*v1.CoreData, error) {

	var temp []*v1.CoreData

	if strings.Contains(data, soh) && strings.Contains(data, eot) {

		tempstrs := strings.Split(data, eot)
		for _, v := range tempstrs {
			if v != "" {
				temphb := v1.CoreData{
					Item: make(map[string]*v1.Item),
				}
				strs := strings.Split(v, etx)
				tempHeadstr, templocation, tempsensor := HB2ProtocHeader(strs[0])
				if tempHeadstr != "" {
					temphb.Msgtype = tempHeadstr
					temphb.Lid = templocation
					temphb.Sid = tempsensor
				} else {
					return temp, fmt.Errorf("航标九解析，数据头获取失败,数据头为:%s", strs[0])
				}
				for i, v := range strs {
					if i == 0 || v == "" {
						continue
					}
					key, value, err := HB2ProtocBody(v)
					if err != nil {
						return temp, fmt.Errorf("航标九解析，获取数据项 getbody错误,数据项为:%s", v)
					}

					temphb.Item[key] = value
				}
				return temp, nil
			} else {
				return temp, fmt.Errorf("航标九解析错误")
			}
		}
	} else {
		return temp, fmt.Errorf("航标九解析，接收到不完整数据，无SOH,EOT ,data = %s", data)
	}
	return temp, nil
}

func HB2ProtocHeader(data string) (string, int32, int32) {
	if data != "" {
		strs := strings.Split(data, sep)
		if len(strs) == 6 {
			lid, err := strconv.Atoi(strs[5])
			if err != nil {
				return "", 0, 0
			}
			sid, err := strconv.Atoi(strs[4])
			if err != nil {
				return "", 0, 0
			}
			return strs[3], int32(lid), int32(sid)
		} else {

			return "", 0, 0
		}
	} else {

		return "", 0, 0
	}
}

func HB2ProtocBody(data string) (string, *v1.Item, error) {
	strs := strings.Split(data, sep)
	item := &v1.Item{}
	if len(strs) == 5 {
		key := strings.Trim(strs[0], stx)
		if key == "" {
			return "", nil, fmt.Errorf("航标九解析，数据项key为空,数据项为:%s", data)
		}
		item.A = ""
		item.N = strs[0]
		item.S = strs[2]
		item.T = strs[1]
		item.V = strs[3]
		item.U = strs[4]
		return key, item, nil
	} else {
		return "", nil, fmt.Errorf("航标九解析，数据项长度错误,数据项为:%s", data)
	}
}
