package server

import (
	"server/conf"

	"github.com/go-kratos/examples/ws/handler"
	"github.com/go-kratos/kratos/v2/transport/http"
	"github.com/gorilla/mux"
)

func NewWebsocketServer(conf *conf.Other) *http.Server {
	router := mux.NewRouter()
	router.HandleFunc("/ws", handler.WsHandler)

	httpSrv := http.NewServer(http.Address(conf.Dataws.Addr))
	httpSrv.HandlePrefix("/", router)

	return httpSrv
}
