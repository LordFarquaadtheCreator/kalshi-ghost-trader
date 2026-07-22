package httpapi

import "encoding/json"

// jsonNewDecoder wraps json.NewDecoder for test use.
func jsonNewDecoder(r interface{ Read([]byte) (int, error) }) *json.Decoder {
	return json.NewDecoder(readerAdapter{r})
}

type readerAdapter struct {
	r interface{ Read([]byte) (int, error) }
}

func (a readerAdapter) Read(p []byte) (int, error) {
	return a.r.Read(p)
}
