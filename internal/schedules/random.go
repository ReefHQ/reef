package schedules

import (
	"crypto/rand"
	"encoding/hex"
)

func newID() string {
	b := make([]byte, 24)
	if _, e := rand.Read(b); e != nil {
		panic(e)
	}
	return hex.EncodeToString(b)
}
