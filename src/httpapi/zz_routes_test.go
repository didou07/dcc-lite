package httpapi

import "testing"

func TestRoutesNoPanic(t *testing.T) {
	s := &Server{}
	_ = s.routes()
}
