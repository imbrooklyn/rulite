package dynamic

import (
	"bytes"
	"encoding/json/jsontext"
	"errors"
	"io"
)

const (
	maxDocumentBytes = 1 << 20
	maxParamsBytes   = 16 << 10
	maxDefinitions   = 256
)

// checkJSON uses the standard decoder's duplicate-name and Unicode checks.
// Token traversal enforces limits before semantic decoding can allocate a tree.
func checkJSON(data []byte, root jsontext.Kind, byteLimit, depthLimit, tokenLimit int) error {
	if len(data) > byteLimit {
		return ErrLimit
	}
	d := jsontext.NewDecoder(bytes.NewReader(data))
	for n := 0; ; n++ {
		token, err := d.ReadToken()
		if err != nil {
			return err
		}
		if n == 0 && token.Kind() != root {
			return errors.New("unexpected JSON root kind")
		}
		if n >= tokenLimit || d.StackDepth() > depthLimit {
			return ErrLimit
		}
		if token.Kind() == 'n' {
			return errors.New("JSON null is not supported; omit optional fields")
		}
		if d.StackDepth() == 0 {
			break
		}
	}
	if _, err := d.ReadToken(); err != io.EOF {
		if err != nil {
			return err
		}
		return errors.New("trailing JSON data")
	}
	return nil
}
