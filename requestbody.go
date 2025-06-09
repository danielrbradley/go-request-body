package requestbody

import (
	"compress/gzip"
	"context"
	"io"
	"net/http"
	"sync"
)

func RequestBodyHandler(h http.Handler, defaults ...Option) http.Handler {
	defaultOptions := options{}
	for _, opt := range defaults {
		opt.apply(&defaultOptions)
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Short circuit if the request method does not have a body.
		// Go's internal http package will already limit the reader to specified content length.
		if r.ContentLength == 0 {
			h.ServeHTTP(w, r)
			return
		}

		contentEncoding := r.Header.Get("Content-Encoding")
		hasGzipEncoding := contentEncoding == "gzip" || contentEncoding == "x-gzip"

		lazyBody := &lazyReader{
			hasGzipEncoding: hasGzipEncoding,
			reader:          r.Body,
			options:         defaultOptions,
			request:         r,
			writer:          w,
		}

		r = r.WithContext(context.WithValue(r.Context(), contextKey, &lazyBody.options))
		r.Body = lazyBody

		h.ServeHTTP(w, r)
	})
}

type contextType struct{}

var contextKey = contextType{}

type Option interface {
	apply(*options)
}

type options struct {
	defaultMaxContentLength int64
	disableGzip             bool
	onError                 func(w http.ResponseWriter, r *http.Request, err error) error
}

func MaxContentLength(maxContentLength int64) Option {
	return optionFunc{
		f: func(opts *options) {
			opts.defaultMaxContentLength = maxContentLength
		},
	}
}

func DisableGzip() Option {
	return optionFunc{
		f: func(opts *options) {
			opts.disableGzip = true
		},
	}
}

func EnableGzip() Option {
	return optionFunc{
		f: func(opts *options) {
			opts.disableGzip = false
		},
	}
}

func OnError(fn func(w http.ResponseWriter, r *http.Request, err error) error) Option {
	return optionFunc{
		f: func(opts *options) {
			opts.onError = fn
		},
	}
}

func Set(r *http.Request, opts ...Option) {
	if r == nil {
		return
	}
	if ctx := r.Context(); ctx != nil {
		if existingOpts, ok := ctx.Value(contextKey).(*options); ok {
			for _, opt := range opts {
				opt.apply(existingOpts)
			}
		}
	}
}

type optionFunc struct {
	f func(*options)
}

func (o optionFunc) apply(opts *options) {
	o.f(opts)
}

type lazyReader struct {
	hasGzipEncoding bool
	once            sync.Once
	reader          io.ReadCloser
	initErr         error
	options         options
	request         *http.Request
	writer          http.ResponseWriter
}

func (r *lazyReader) Read(p []byte) (n int, err error) {
	// On first read, perform initialization of reader based on options.
	r.once.Do(func() {
		reader := r.reader
		if r.hasGzipEncoding && !r.options.disableGzip {
			// If gzip encoding is enabled, wrap the reader with a gzip reader.
			reader, r.initErr = gzip.NewReader(r.reader)
			if r.initErr != nil {
				return
			}
		}
		if r.options.defaultMaxContentLength > 0 {
			// Limit the reader to the specified max content length.
			reader = http.MaxBytesReader(r.writer, reader, r.options.defaultMaxContentLength)
		}
		r.reader = reader
	})

	if r.initErr != nil {
		if r.options.onError != nil {
			return 0, r.options.onError(r.writer, r.request, r.initErr)
		}
		return 0, r.initErr
	}
	n, err = r.reader.Read(p)
	if err != nil && r.options.onError != nil {
		return n, r.options.onError(r.writer, r.request, err)
	}
	return n, err
}

func (r *lazyReader) Close() error {
	if r.initErr != nil {
		if r.options.onError != nil {
			return r.options.onError(r.writer, r.request, r.initErr)
		}
		return r.initErr
	}
	return r.reader.Close()
}
