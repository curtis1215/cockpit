package sources

import (
	"net/http"
	"testing"

	"github.com/curtis1215/cockpit/internal/inventory"
)

func TestPypi(t *testing.T) {
	s, hc := srv(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`{"info":{"version":"1.4.2"}}`)) })
	defer s.Close()
	res, err := fetchPypi(inventory.Software{Name: "x", LatestSource: "pypi:p"}, "p", hc, s.URL)
	if err != nil || res.Version != "1.4.2" {
		t.Fatalf("pypi: %+v %v", res, err)
	}
}
func TestBrew(t *testing.T) {
	s, hc := srv(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(`{"versions":{"stable":"3.2.1"}}`)) })
	defer s.Close()
	res, err := fetchBrew(inventory.Software{Name: "x", LatestSource: "brew:w"}, "w", hc, s.URL)
	if err != nil || res.Version != "3.2.1" {
		t.Fatalf("brew: %+v %v", res, err)
	}
}
func TestCustom(t *testing.T) {
	res, err := fetchCustom(inventory.Software{Name: "x", LatestSource: "custom:echo 9.9.9"}, "echo 9.9.9")
	if err != nil || res.Version != "9.9.9" {
		t.Fatalf("custom: %+v %v", res, err)
	}
	if _, err := fetchCustom(inventory.Software{}, "exit 7"); err == nil {
		t.Fatal("custom nonzero should error")
	}
}
