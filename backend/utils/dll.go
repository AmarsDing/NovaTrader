package utils

import (
	"github.com/dop251/goja"
)

type DLL struct {
	Id     string
	Name   string
	Code   string
	vm     *goja.Runtime
}

func NewDll(code string) {

}
