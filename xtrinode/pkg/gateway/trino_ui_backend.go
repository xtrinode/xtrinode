package gateway

import (
	"net/url"
	"strings"
)

func (gs *GatewayService) defaultTrinoUIBackend() (RouteEntry, Backend, bool) {
	routes := gs.uniqueRouteSnapshot()
	for i := range routes {
		if !routes[i].Default {
			continue
		}
		if backend := gs.selectBackend(&routes[i]); backend != nil {
			return routes[i], *backend, true
		}
		if backend := gs.pickResumeCandidate(&routes[i]); backend != nil {
			return routes[i], *backend, true
		}
	}

	type candidate struct {
		route   RouteEntry
		backend Backend
	}
	selectable := make(map[string]*candidate)
	for i := range routes {
		for j := range routes[i].Backends {
			backend := routes[i].Backends[j]
			if !gs.isBackendSelectable(&backend) {
				continue
			}
			selectable[backendIdentityKey(&backend)] = &candidate{route: routes[i], backend: backend}
		}
	}
	if len(selectable) == 1 {
		for _, candidate := range selectable {
			return candidate.route, candidate.backend, true
		}
	}
	if len(selectable) > 1 {
		return RouteEntry{}, Backend{}, false
	}

	resumable := make(map[string]*candidate)
	for i := range routes {
		for j := range routes[i].Backends {
			backend := routes[i].Backends[j]
			if !trinoUIResumeCandidate(&backend) {
				continue
			}
			resumable[backendIdentityKey(&backend)] = &candidate{route: routes[i], backend: backend}
		}
	}
	if len(resumable) != 1 {
		return RouteEntry{}, Backend{}, false
	}
	for _, candidate := range resumable {
		return candidate.route, candidate.backend, true
	}
	return RouteEntry{}, Backend{}, false
}

func trinoUIResumeCandidate(backend *Backend) bool {
	if !backend.Active || backend.Name == "" || backend.Namespace == "" {
		return false
	}
	_, ok := resumeStatePriority(backend.State)
	return ok
}

func isDefaultTrinoUIPath(path string) bool {
	trimmed := strings.TrimPrefix(path, TrinoUIPath+"/")
	if trimmed == "" || trimmed == path {
		return false
	}
	first, _, _ := strings.Cut(trimmed, "/")
	first, err := url.PathUnescape(first)
	if err != nil || first == "" {
		return false
	}
	switch first {
	case "assets", "vendor", "api", "login", "logout":
		return true
	}
	return strings.HasSuffix(first, ".html") ||
		strings.HasSuffix(first, ".css") ||
		strings.HasSuffix(first, ".js") ||
		strings.HasSuffix(first, ".ico")
}

func (gs *GatewayService) resolveTrinoUIBackend(path string) (route RouteEntry, backend Backend, targetPath string, ok, ambiguous bool) {
	trimmed := strings.TrimPrefix(path, TrinoUIPath+"/")
	if trimmed == "" || trimmed == path {
		return RouteEntry{}, Backend{}, "", false, false
	}

	segments := strings.Split(trimmed, "/")
	if len(segments) >= 2 {
		namespace, namespaceOK := url.PathUnescape(segments[0])
		name, nameOK := url.PathUnescape(segments[1])
		if namespaceOK == nil && nameOK == nil && namespace != "" && name != "" {
			route, backend, ok, _ = gs.findTrinoUIBackend(namespace, name)
			if ok {
				return route, backend, trinoUITargetPath(segments[2:]), true, false
			}
		}
	}

	name, err := url.PathUnescape(segments[0])
	if err != nil || name == "" {
		return RouteEntry{}, Backend{}, "", false, false
	}
	route, backend, ok, ambiguous = gs.findTrinoUIBackend("", name)
	if !ok || ambiguous {
		return RouteEntry{}, Backend{}, "", ok, ambiguous
	}
	return route, backend, trinoUITargetPath(segments[1:]), true, false
}

func (gs *GatewayService) findTrinoUIBackend(namespace, name string) (RouteEntry, Backend, bool, bool) {
	routes := gs.uniqueRouteSnapshot()
	type match struct {
		route   RouteEntry
		backend Backend
	}
	matches := make([]match, 0, 1)
	seen := make(map[string]struct{})
	for i := range routes {
		for j := range routes[i].Backends {
			backend := routes[i].Backends[j]
			if backend.Name != name {
				continue
			}
			if namespace != "" && backend.Namespace != namespace {
				continue
			}
			key := backendIdentityKey(&backend)
			if _, exists := seen[key]; exists {
				continue
			}
			seen[key] = struct{}{}
			matches = append(matches, match{route: routes[i], backend: backend})
		}
	}
	if len(matches) == 0 {
		return RouteEntry{}, Backend{}, false, false
	}
	if namespace == "" && len(matches) > 1 {
		return RouteEntry{}, Backend{}, true, true
	}
	return matches[0].route, matches[0].backend, true, false
}

func trinoUITargetPath(segments []string) string {
	if len(segments) == 0 || strings.Join(segments, "/") == "" {
		return "/ui/"
	}
	return "/ui/" + strings.Join(segments, "/")
}

func backendIdentityKey(backend *Backend) string {
	return backend.Namespace + "\x00" + backend.Name + "\x00" + backend.CoordinatorURL
}
