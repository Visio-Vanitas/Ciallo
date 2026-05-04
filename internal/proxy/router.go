package proxy

import "ciallo/internal/mcproto"

type StaticRouter struct {
	routes map[string]Backend
	def    *Backend
}

func NewStaticRouter(routes []Route, def *Backend) *StaticRouter {
	routeMap := make(map[string]Backend)
	for _, route := range routes {
		for _, host := range route.Hosts {
			normalized := NormalizeHost(host)
			if normalized != "" {
				routeMap[normalized] = route.Backend
			}
		}
	}
	return &StaticRouter{
		routes: routeMap,
		def:    def,
	}
}

func (r *StaticRouter) Resolve(host string) (Backend, bool) {
	normalized := NormalizeHost(host)
	if backend, ok := r.routes[normalized]; ok {
		return backend, true
	}
	if r.def != nil {
		return *r.def, true
	}
	return Backend{}, false
}

func NormalizeHost(host string) string {
	return mcproto.NormalizeHost(host)
}
