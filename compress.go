package fasthttp

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"fmt"
	"io"
	"os"
	"sync"
)

// Supported compression levels.
const (
	CompressNoCompression      = flate.NoCompression
	CompressBestSpeed          = flate.BestSpeed
	CompressBestCompression    = flate.BestCompression
	CompressDefaultCompression = flate.DefaultCompression
)

func acquireGzipReader(r io.Reader) (*gzip.Reader, error) {
	v := gzipReaderPool.Get()
	if v == nil {
		return gzip.NewReader(r)
	}
	zr := v.(*gzip.Reader)
	if err := zr.Reset(r); err != nil {
		return nil, err
	}
	return zr, nil
}

func releaseGzipReader(zr *gzip.Reader) {
	zr.Close()
	gzipReaderPool.Put(zr)
}

var gzipReaderPool sync.Pool

func acquireFlateReader(r io.Reader) (io.ReadCloser, error) {
	v := flateReaderPool.Get()
	if v == nil {
		zr := flate.NewReader(r)
		return zr, nil
	}
	zr := v.(io.ReadCloser)
	if err := resetFlateReader(zr, r); err != nil {
		return nil, err
	}
	return zr, nil
}

func releaseFlateReader(zr io.ReadCloser) {
	zr.Close()
	flateReaderPool.Put(zr)
}

func resetFlateReader(zr io.ReadCloser, r io.Reader) error {
	zrr, ok := zr.(flate.Resetter)
	if !ok {
		panic("BUG: flate.Reader doesn't implement flate.Resetter???")
	}
	return zrr.Reset(r, nil)
}

var flateReaderPool sync.Pool

func acquireGzipWriter(w io.Writer, level int) *gzipWriter {
	p := gzipWriterPoolMap[level]
	if p == nil {
		panic(fmt.Sprintf("BUG: unexpected compression level passed: %d. See compress/gzip for supported levels", level))
	}

	v := p.Get()
	if v == nil {
		zw, err := gzip.NewWriterLevel(w, level)
		if err != nil {
			panic(fmt.Sprintf("BUG: unexpected error from gzip.NewWriterLevel(%d): %s", level, err))
		}
		return &gzipWriter{
			Writer: zw,
			p:      p,
		}
	}
	zw := v.(*gzipWriter)
	zw.Reset(w)
	return zw
}

func releaseGzipWriter(zw *gzipWriter) {
	zw.Close()
	zw.p.Put(zw)
}

type gzipWriter struct {
	*gzip.Writer
	p *sync.Pool
}

var gzipWriterPoolMap = func() map[int]*sync.Pool {
	// Initialize pools for all the compression levels defined
	// in https://golang.org/pkg/compress/gzip/#pkg-constants .
	m := make(map[int]*sync.Pool, 11)
	m[-1] = &sync.Pool{}
	for i := 0; i < 10; i++ {
		m[i] = &sync.Pool{}
	}
	return m
}()

func acquireFlateWriter(w io.Writer, level int) *flateWriter {
	p := flateWriterPoolMap[level]
	if p == nil {
		panic(fmt.Sprintf("BUG: unexpected compression level passed: %d. See compress/flate for supported levels", level))
	}

	v := p.Get()
	if v == nil {
		zw, err := flate.NewWriter(w, level)
		if err != nil {
			panic(fmt.Sprintf("BUG: unexpected error in flate.NewWriter(%d): %s", level, err))
		}
		return &flateWriter{
			Writer: zw,
			p:      p,
		}
	}
	zw := v.(*flateWriter)
	zw.Reset(w)
	return zw
}

func releaseFlateWriter(zw *flateWriter) {
	zw.Close()
	zw.p.Put(zw)
}

type flateWriter struct {
	*flate.Writer
	p *sync.Pool
}

var flateWriterPoolMap = func() map[int]*sync.Pool {
	// Initialize pools for all the compression levels defined
	// in https://golang.org/pkg/compress/flate/#pkg-constants .
	m := make(map[int]*sync.Pool, 11)
	m[-1] = &sync.Pool{}
	for i := 0; i < 10; i++ {
		m[i] = &sync.Pool{}
	}
	return m
}()

func isFileCompressible(f *os.File, minCompressRatio float64) bool {
	// Try compressing the first 4kb of of the file
	// and see if it can be compressed by more than
	// the given minCompressRatio.
	var buf bytes.Buffer
	zw := acquireGzipWriter(&buf, CompressDefaultCompression)
	lr := &io.LimitedReader{
		R: f,
		N: 4096,
	}
	_, err := io.Copy(zw, lr)
	releaseGzipWriter(zw)
	f.Seek(0, 0)
	if err != nil {
		return false
	}

	n := 4096 - lr.N
	zn := len(buf.Bytes())
	return float64(zn) < float64(n)*minCompressRatio
}
