package proxy

import (
	"net"
	"sync"
	"time"
)

type PipeStats struct {
	ClientToBackend int64
	BackendToClient int64
}

func ProxyBidirectional(client, backend net.Conn, idleTimeout time.Duration) PipeStats {
	var stats PipeStats
	var once sync.Once
	done := make(chan struct{}, 2)

	closeBoth := func() {
		_ = client.Close()
		_ = backend.Close()
	}

	copyOne := func(dst, src net.Conn, counter *int64) {
		*counter = copyWithIdle(dst, src, idleTimeout)
		once.Do(closeBoth)
		done <- struct{}{}
	}

	go copyOne(backend, client, &stats.ClientToBackend)
	go copyOne(client, backend, &stats.BackendToClient)

	<-done
	<-done
	return stats
}

func copyWithIdle(dst, src net.Conn, idleTimeout time.Duration) int64 {
	buf := make([]byte, 32*1024)
	var written int64
	for {
		if idleTimeout > 0 {
			_ = src.SetReadDeadline(time.Now().Add(idleTimeout))
		}
		nr, readErr := src.Read(buf)
		if nr > 0 {
			if idleTimeout > 0 {
				_ = dst.SetWriteDeadline(time.Now().Add(idleTimeout))
			}
			nw, writeErr := dst.Write(buf[:nr])
			written += int64(nw)
			if writeErr != nil || nw != nr {
				return written
			}
		}
		if readErr != nil {
			return written
		}
	}
}
