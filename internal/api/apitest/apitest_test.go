package apitest

import (
	"context"
	"testing"

	"github.com/ape-dev-de/tiegel/internal/api"
)

func TestCallers(t *testing.T) {
	a := NewApp(t, map[string]string{"dir/a.txt": "x"})
	for name, c := range Callers(t, a) {
		res, err := api.Call[api.DocOpenResult](context.Background(), c, "doc.open", api.DocOpenParams{Path: "dir/a.txt", Text: true})
		if err != nil || res.Text != "x" {
			t.Fatalf("%s: %+v %v", name, res, err)
		}
	}
}
