/*
 * @Author: hu 2245749018@qq.com
 * @Date: 2022-08-11 17:37:37
 * @LastEditors: hu 2245749018@qq.com
 * @LastEditTime: 2022-08-11 18:48:01
 * @FilePath: \weatherMaster-client\internal\pkg\log\log.go
 * @Description:
 *
 * Copyright (c) 2022 by hu 2245749018@qq.com, All Rights Reserved.
 */
package log

import (
	"server/conf"

	"github.com/go-kratos/kratos/v2/log"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

//makeZapOptions 通过配置创建 zap 的配置选项
func makeZapOptions(conf *conf.Log) []zap.Option {
	var options = make([]zap.Option, 4)
	options = options[0:0]

	if conf.Logger.Development {
		options = append(options, zap.Development())
	}
	if conf.Logger.Caller {
		options = append(options, zap.AddCaller())
	}

	options = append(options, zap.AddCallerSkip(int(conf.Logger.CallerSkip)))
	// options = append(options, zap.AddStacktrace(zap.NewAtomicLevelAt(zapcore.Level(conf.Logger.StackLevel))))

	return options
}

//makeLogger 创建日志接口
func Logger(conf *conf.Log, kv ...interface{}) log.Logger {
	encoder := zapcore.EncoderConfig{
		TimeKey:        "ts",
		LevelKey:       "level",
		NameKey:        "logger",
		CallerKey:      "caller",
		MessageKey:     "msg",
		StacktraceKey:  "stack",
		EncodeTime:     zapcore.ISO8601TimeEncoder,
		LineEnding:     zapcore.DefaultLineEnding,
		EncodeLevel:    zapcore.LowercaseLevelEncoder,
		EncodeDuration: zapcore.SecondsDurationEncoder,
		EncodeCaller:   zapcore.FullCallerEncoder,
	}
	options := makeZapOptions(conf)
	zapLogger := NewZapLogger(
		conf,
		encoder,
		zap.NewAtomicLevelAt(zapcore.Level(conf.Logger.Level)),
		options...,
	)

	logger := log.With(zapLogger,
		kv...,
	)

	return logger
}
