package stubborn

import (
	"bytes"
	"compress/flate"
	"compress/gzip"
	"io"
)

func GZipDecompress(input []byte) ([]byte, error) {
	buf := bytes.NewBuffer(input)
	reader, gzipErr := gzip.NewReader(buf)
	if gzipErr != nil {
		return nil, gzipErr
	}
	defer reader.Close()

	buf = bytes.NewBuffer(nil)
	_, err := io.Copy(buf, reader)
	if err != nil {
		return nil, err
	}

	return buf.Bytes(), nil
}

func FlateDecompress(input []byte) ([]byte, error) {
	reader := flate.NewReader(bytes.NewReader(input))
	defer reader.Close()

	buf := bytes.NewBuffer(nil)
	_, err := io.Copy(buf, reader)
	if err != nil {
		return nil, err
	}

	return buf.Bytes(), nil
}

func GZipCompress(input string) ([]byte, error) {
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)

	_, err := gz.Write([]byte(input))
	if err != nil {
		return nil, err
	}

	err = gz.Flush()
	if err != nil {
		return nil, err
	}

	err = gz.Close()
	if err != nil {
		return nil, err
	}

	return buf.Bytes(), nil
}
