package geolocation

import (
	"context"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLookupSelfResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			t.Errorf("lookup must use caller's IP, not an address in the URL: %s", r.URL.Path)
		}
		w.Write([]byte(`{"success":true,"ip":"8.8.8.8","city":"Test city","country":"Test country","latitude":51.5,"longitude":-0.12}`))
	}))
	defer server.Close()
	place, err := lookup(context.Background(), server.Client(), server.URL+"/")
	if err != nil || place.Name != "Test city" || place.Lat != 51.5 || place.Lon != -0.12 {
		t.Fatalf("place=%+v err=%v", place, err)
	}
}

func TestLookupRejectsFailedOrInvalidResponses(t *testing.T) {
	for _, body := range []string{`{"success":false}`, `not-json`, `{"success":true,"city":"Missing coords"}`, `{"success":true,"city":"Bad","latitude":91,"longitude":0}`, strings.Repeat("x", 9000)} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte(body)) }))
		if place, err := lookup(context.Background(), server.Client(), server.URL); err == nil {
			t.Errorf("accepted response: %+v", place)
		}
		server.Close()
	}
}

func TestPlaceValidation(t *testing.T) {
	for _, place := range []Place{{Name: ""}, {Name: "Bad\nname"}, {Name: "City", Lat: math.NaN()}, {Name: "City", Lon: math.Inf(1)}, {Name: "City", Lon: 181}} {
		if place.Validate() == nil {
			t.Errorf("accepted invalid place %+v", place)
		}
	}
	if err := (Place{Name: "Zero coordinates", Lat: 0, Lon: 0}).Validate(); err != nil {
		t.Fatal(err)
	}
}
