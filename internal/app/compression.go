package app

import (
	"compress/gzip"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

const maxReplayJSONBytes = 2 << 20

func decodeReplayJSON(w http.ResponseWriter, r *http.Request, target any) error {
	// Bound the compressed upload first, then bound the decompressed stream as a
	// second defense against oversized payloads / decompression bombs.
	r.Body = http.MaxBytesReader(w, r.Body, 768<<10)
	var reader io.Reader = r.Body
	if strings.EqualFold(strings.TrimSpace(r.Header.Get("Content-Encoding")), "gzip") {
		gz, err := gzip.NewReader(reader)
		if err != nil {
			return fmt.Errorf("invalid gzip body: %w", err)
		}
		defer gz.Close()
		reader = gz
	}
	limited := io.LimitReader(reader, maxReplayJSONBytes+1)
	dec := json.NewDecoder(limited)
	if err := dec.Decode(target); err != nil {
		return err
	}
	// Reject a second top-level JSON value. This also forces the reader to notice
	// when the decompressed stream exceeds the configured limit.
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		if err == nil {
			return fmt.Errorf("multiple JSON values are not allowed")
		}
		return fmt.Errorf("payload exceeds replay limit or contains trailing data: %w", err)
	}
	return nil
}
