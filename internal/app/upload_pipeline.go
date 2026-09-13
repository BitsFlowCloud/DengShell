package app

import (
	"context"
	"io"
	"net/http"
	"sync"
	"sync/atomic"
	"time"
)

const uploadChunkSize = 32 * 1024
const uploadConcurrency = 32

type cancellableHTTPUploadBody struct {
	io.ReadCloser
	controller *http.ResponseController
	once       sync.Once
	err        error
}

func (b *cancellableHTTPUploadBody) Close() error {
	b.once.Do(func() {
		// net/http's request body Read and Close share a mutex. Interrupt the
		// socket read first; Close alone cannot release a partially sent body.
		_ = b.controller.SetReadDeadline(time.Now())
		b.err = b.ReadCloser.Close()
	})
	return b.err
}

func protectHTTPUploadBody(ctx context.Context, w http.ResponseWriter, body io.ReadCloser) (io.ReadCloser, func()) {
	input := &cancellableHTTPUploadBody{ReadCloser: body, controller: http.NewResponseController(w)}
	closed := make(chan struct{})
	stop := context.AfterFunc(ctx, func() { _ = input.Close(); close(closed) })
	var once sync.Once
	return input, func() {
		once.Do(func() {
			if !stop() {
				<-closed
			}
		})
	}
}

// uploadChunks uses a 1 MiB chunk-buffer pool and at most 32 outstanding SFTP
// WRITE requests. The SFTP/SSH libraries have their own protocol buffers.
// WriteAt returns only after the server acknowledges the bytes;
// progress therefore measures remote writes, not local reads or queued data.
// Every caller writes a unique temporary file and commits only after all writes
// succeed, so out-of-order completion cannot expose a partially uploaded target.
func uploadChunks(parent context.Context, destination io.WriterAt, source io.Reader, acknowledge func(int)) (int64, error) {
	ctx, cancel := context.WithCancelCause(parent)
	defer cancel(nil)
	if closer, ok := source.(io.Closer); ok {
		closed := make(chan struct{})
		stop := context.AfterFunc(ctx, func() { _ = closer.Close(); close(closed) })
		defer func() {
			if !stop() {
				<-closed
			}
		}()
	}
	type chunk struct {
		data   []byte
		offset int64
	}
	free := make(chan []byte, uploadConcurrency)
	for range uploadConcurrency {
		free <- make([]byte, uploadChunkSize)
	}
	jobs := make(chan chunk)
	var workers sync.WaitGroup
	var acknowledged atomic.Int64
	for range uploadConcurrency {
		workers.Go(func() {
			for item := range jobs {
				if ctx.Err() == nil {
					n, err := destination.WriteAt(item.data, item.offset)
					if n < 0 || n > len(item.data) {
						n, err = 0, io.ErrShortWrite
					}
					if n > 0 {
						acknowledged.Add(int64(n))
						if acknowledge != nil {
							acknowledge(n)
						}
					}
					if err == nil && n != len(item.data) {
						err = io.ErrShortWrite
					}
					if err != nil {
						cancel(err)
					}
				}
				free <- item.data[:uploadChunkSize]
			}
		})
	}
	var offset int64
readLoop:
	for {
		var buffer []byte
		select {
		case <-ctx.Done():
			break readLoop
		case buffer = <-free:
		}
		if ctx.Err() != nil {
			free <- buffer
			break
		}
		n, err := io.ReadFull(source, buffer)
		if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
			free <- buffer
			cancel(err)
			break
		}
		if n > 0 {
			select {
			case <-ctx.Done():
				free <- buffer
				break readLoop
			case jobs <- chunk{buffer[:n], offset}:
				offset += int64(n)
			}
		} else {
			free <- buffer
		}
		if err != nil {
			break
		}
	}
	close(jobs)
	// Pending SFTP requests must finish before closing/removing the temporary
	// file. Disconnecting the SSH session also wakes these requests with errors.
	workers.Wait()
	return acknowledged.Load(), context.Cause(ctx)
}
