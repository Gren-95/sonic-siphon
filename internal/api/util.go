package api

import (
	"os"
	"time"
)

var nowFn = time.Now

func removeIfExists(path string) error {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil
	}
	return os.Remove(path)
}
