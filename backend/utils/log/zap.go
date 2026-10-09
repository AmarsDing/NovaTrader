/*
 * @Author: hu 2245749018@qq.com
 * @Date: 2022-08-11 17:37:37
 * @LastEditors: hu 2245749018@qq.com
 * @LastEditTime: 2022-08-11 17:54:18
 * @FilePath: \weatherMaster-client\internal\pkg\log\zap.go
 * @Description:
 *
 * Copyright (c) 2022 by hu 2245749018@qq.com, All Rights Reserved.
 */
package log

import (
	"server/conf"
	"fmt"
	"os"

	"github.com/go-kratos/kratos/v2/log"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"gopkg.in/natefinch/lumberjack.v2"
)

var _ log.Logger = (*ZapLogger)(nil)

// ZapLogger is a logger impl.
type ZapLogger struct {
	log  *zap.Logger
	Sync func() error
}

// NewZapLogger return a zap logger.
func NewZapLogger(conf *conf.Log, encoder zapcore.EncoderConfig, level zap.AtomicLevel, opts ...zap.Option) *ZapLogger {
	writeSyncer := getLogWriter(conf)

	core := zapcore.NewCore(
		zapcore.NewConsoleEncoder(encoder),
		zapcore.NewMultiWriteSyncer(
			zapcore.AddSync(os.Stdout),
			zapcore.AddSync(writeSyncer),
		), level)

	zapLogger := zap.New(core, opts...)

	return &ZapLogger{log: zapLogger, Sync: zapLogger.Sync}
}

// Log Implementation of logger interface.
func (l *ZapLogger) Log(level log.Level, keyvals ...interface{}) error {
	if len(keyvals) == 0 || len(keyvals)%2 != 0 {
		l.log.Warn(fmt.Sprint("Keyvalues must appear in pairs: ", keyvals))
		return nil
	}
	// Zap.Field is used when keyvals pairs appear
	var data []zap.Field
	for i := 0; i < len(keyvals); i += 2 {
		data = append(data, zap.Any(fmt.Sprint(keyvals[i]), fmt.Sprint(keyvals[i+1])))
	}
	switch level {
	case log.LevelDebug:
		l.log.Debug("", data...)
	case log.LevelInfo:
		l.log.Info("", data...)
	case log.LevelWarn:
		l.log.Warn("", data...)
	case log.LevelError:
		l.log.Error("", data...)
	}
	return nil
}

// 日志自动切割，采用 lumberjack 实现的
func getLogWriter(conf *conf.Log) zapcore.WriteSyncer {
	lumberJackLogger := &lumberjack.Logger{
		Filename:   conf.File.Filename,
		MaxSize:    int(conf.File.MaxSize),
		MaxBackups: int(conf.File.MaxBackups),
		MaxAge:     int(conf.File.MaxAge),
		Compress:   conf.File.Compress,
	}
	return zapcore.AddSync(lumberJackLogger)
}
