package check

// 输出 func(string)string

import (
	"fmt"

	"github.com/dop251/goja"
)

type DllFunc struct {
	Scripts string              // 读取js文件内容
	Vm      *goja.Runtime       // 运行实例
	Handler func(string) string // 处理函数
}

// 获取goja实例
func NewDllFunc(js string) (*DllFunc, error) {

	dll := &DllFunc{
		Scripts: js,
		Vm:      goja.New(),
	}
	_, err := dll.Vm.RunString(js)
	if err != nil {
		return nil, err
	}

	defer func() { // 必须要先声明defer，否则不能捕获到panic异常
		if err := recover(); err != nil {
			fmt.Println(err)
		}
	}()
	err = dll.Vm.ExportTo(dll.Vm.Get("funcDll"), &dll.Handler)
	if err != nil {
		return nil, err
	}
	return dll, nil
}
