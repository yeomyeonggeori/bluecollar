package model

import (
	"context"
	"sync"
)

type WireExchange struct {
	Endpoint string `json:"endpoint"`
	Request  string `json:"request"`
	Response string `json:"response,omitempty"`
}

type WireCapture struct {
	mutex    sync.Mutex
	exchange *WireExchange
}

type wireCaptureContextKey struct{}

func WithWireCapture(ctx context.Context) (context.Context, *WireCapture) {
	capture := &WireCapture{}
	return context.WithValue(ctx, wireCaptureContextKey{}, capture), capture
}

func RecordWireExchange(ctx context.Context, exchange WireExchange) {
	capture, isCapturing := ctx.Value(wireCaptureContextKey{}).(*WireCapture)
	if !isCapturing {
		return
	}
	capture.mutex.Lock()
	defer capture.mutex.Unlock()
	capture.exchange = &exchange
}

func (capture *WireCapture) Exchange() *WireExchange {
	capture.mutex.Lock()
	defer capture.mutex.Unlock()
	return capture.exchange
}
