package proxy

import "testing"

func TestStaticRouterResolve(t *testing.T) {
	def := Backend{Name: "default", Addr: "127.0.0.1:25566"}
	router := NewStaticRouter([]Route{
		{
			Hosts: []string{"Survival.Example.Com."},
			Backend: Backend{
				Name: "survival",
				Addr: "127.0.0.1:25567",
			},
		},
	}, &def)

	got, ok := router.Resolve("survival.example.com:25565")
	if !ok || got.Name != "survival" {
		t.Fatalf("got %#v ok=%v", got, ok)
	}
	got, ok = router.Resolve("unknown.example.com")
	if !ok || got.Name != "default" {
		t.Fatalf("default got %#v ok=%v", got, ok)
	}
}
