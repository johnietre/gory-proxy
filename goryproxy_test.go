package goryproxy

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestProxy(t *testing.T) {
	proxyRouter := NewRouterHandler()
	proxy := httptest.NewServer(proxyRouter)
	defer proxy.Close()

	srvr1 := httptest.NewServer(makeHandler("srvr1"))
	defer srvr1.Close()
	srvr2 := httptest.NewServer(makeHandler("srvr2"))
	defer srvr2.Close()

	ps1 := &Server{
		Name: "srvr1",
		Path: "srvr1",
		Addr: srvr1.URL,
	}
	if err := ps1.AddNewProxy(srvr1.URL); err != nil {
		panic(err)
	}
	if err := proxyRouter.AddServer(ps1); err != nil {
		panic(err)
	}

	checkGet(proxy.URL, "srvr1", t)
	checkGetFail(proxy.URL, "srvr2", t)

	ps2 := &Server{
		Name: "srvr2",
		Path: "srvr2",
		Addr: srvr2.URL,
	}
	if err := ps2.AddNewProxy(srvr2.URL); err != nil {
		panic(err)
	}
	if err := proxyRouter.AddServer(ps2); err != nil {
		panic(err)
	}

	checkGet(proxy.URL, "srvr1", t)
	checkGet(proxy.URL, "srvr2", t)

	srvrs := proxyRouter.GetServers()
	ps1 = srvrs["srvr1"]
	if ps1 == nil {
		t.Fatalf("nil srvr1")
	}
	if err := proxyRouter.DeleteServer(ps1); err != nil {
		panic(err)
	}

	checkGetFail(proxy.URL, "srvr1", t)
	checkGet(proxy.URL, "srvr2", t)
}

func checkGet(addr, name string, t *testing.T) {
	resp, err := http.Get(addr + "/" + name)
	if err != nil {
		t.Fatalf("error getting %s: %v", name, err)
	}

	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		t.Fatal("error reading body: ", err)
	}

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d (body: %s)", resp.StatusCode, body)
	}

	bodyStr := string(body)
	if bodyStr != name {
		t.Fatalf("expected %s, got %s", name, bodyStr)
	}
}

func checkGetFail(addr, name string, t *testing.T) {
	resp, err := http.Get(addr + "/" + name)
	if err != nil {
		t.Fatalf("error getting %s: %v", name, err)
	}
	body, err := io.ReadAll(resp.Body)
	resp.Body.Close()
	if err != nil {
		t.Fatal("error reading body: ", err)
	}

	if resp.StatusCode == http.StatusOK {
		t.Fatalf("expected non-200, got 200 (body: %s)", body)
	}
}

func makeHandler(msg string) http.HandlerFunc {
	bmsg := []byte(msg)
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write(bmsg)
	})
}
