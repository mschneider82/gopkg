// Package gopkg implements quick & simple go vanity package import paths.
//
// Vanity go package import paths give a cleaner appearance to go projects by separating the source code location from
// the import path. It also gives flexibility to developers by allowing them to change a project's source code hosting
// platform without requiring the project to be renamed.
// Finally, it allows projects hosted on various platforms to be grouped under a common import path.
//
// Within a Caddyfile, new go packages are added using the gopkg directive:
//
//     gopkg <path> [<vcs>] <uri>
//
// The <path> argument corresponds to the path component of the vanity import path, e.g. for "magnax.ca/caddy/gopkg",
// the path would be "/caddy/gopkg".
// The <vcs> argument is optional, and defaults to "git". If it is specified, it is used to indicate which version
// control system is being used to manage the source.
// The <uri> argument corresponds to the URL/URL of the source code repository. Any format supported by the given VCS
// and the "go get" tool is can be used, as gopkg does not attempt to validate it.
package gopkg

import (
	"fmt"
	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/caddyconfig/caddyfile"
	"github.com/caddyserver/caddy/v2/caddyconfig/httpcaddyfile"
	"github.com/caddyserver/caddy/v2/modules/caddyhttp"
	"html/template"
	"net/http"
	"strings"
)

// DefaultTemplate is the default HTML template used as a response.
const DefaultTemplate = `<html>
<head>
<meta name="go-import" content="{{.Host}}{{.Path}} {{.Vcs}} {{.URL}}">
</head>
<body>
go get {{.Host}}{{.Path}}
</body>
</html>
`

func init() {
	caddy.RegisterModule(GoPackage{})
	httpcaddyfile.RegisterDirective("gopkg", parseCaddyFile)
}

// GoPackage implements vanity go package import paths.
//
// Vanity go package import paths give a cleaner appearance to go projects by separating the source code location from
// the import path. It also gives flexibility to developers by allowing them to change a project's source code hosting
// platform without requiring the project to be renamed. Finally, it allows projects hosted on various platforms to be
// grouped under a common import path.
type GoPackage struct {
	// Path is the HTTP path component of the vanity import path.
	//
	// Given a vanity import path of `web.site/package/name`, the path would be `/package/name`.
	Path string `json:"path"`

	// Vcs is the version control system used by the package.
	//
	// If empty, the default is `git`.
	// Valid values include `git`, `hg`, `svn`, `bzr`, `cvs`. Basically, any version control system that go knows how to address.
	Vcs string `json:"vcs,omitempty"`

	// URL is the URL of the package's source.
	//
	// This is where the go tool will go to download the source code.
	URL string `json:"url"`

	// Submodules contains optional submodule configurations for packages with multiple modules.
	//
	// Each submodule entry maps a subpath to its specific source URL. If URL is empty,
	// it defaults to the parent package URL.
	Submodules []Submodule `json:"submodules,omitempty"`

	// Template is the template used when returning a response (instead of redirecting).
	Template *template.Template
}

// Submodule represents a submodule within a go package.
type Submodule struct {
	// Path is the submodule path relative to the parent package path.
	Path string `json:"path"`

	// URL is the URL of the submodule's source. If empty, defaults to parent package URL.
	URL string `json:"url,omitempty"`
}

func (m GoPackage) CaddyModule() caddy.ModuleInfo {
	return caddy.ModuleInfo{
		ID: "http.handlers.gopkg",
		New: func() caddy.Module {
			return new(GoPackage)
		},
	}
}

// parseCaddyFile parses the gopkg directive in a caddyfile.
//
// The module is automatically mounted at the path of the go package. This shortens the middleware chain for
// non-gopkg requests.
func parseCaddyFile(h httpcaddyfile.Helper) ([]httpcaddyfile.ConfigValue, error) {
	if !h.Next() {
		return nil, h.ArgErr()
	}

	// Pretend the lookahead never happened
	h.Reset()

	var m = new(GoPackage)
	err := m.UnmarshalCaddyfile(h.Dispenser)
	if err != nil {
		return nil, err
	}

	matcher := caddy.ModuleMap{
		"path": h.JSON(caddyhttp.MatchPath{m.Path, m.Path + "/", m.Path + "/*"}),
	}

	return h.NewRoute(matcher, m), nil

}

// UnmarshalCaddyfile implements caddyfile.Unmarshaler. Syntax:
//
//     gopkg <path> [<vcs>] <uri> {
//         submodule <subpath> [<suburi>]
//     }
//
func (m *GoPackage) UnmarshalCaddyfile(d *caddyfile.Dispenser) error {
	for d.Next() {
		if !d.Args(&m.Path) {
			return d.ArgErr()
		}

		args := d.RemainingArgs()
		switch len(args) {
		case 2:
			m.Vcs = args[0]
			args = args[1:]
			fallthrough
		case 1:
			m.URL = args[0]
		default:
			return d.ArgErr()
		}

		// Parse optional block for submodules
		for d.NextBlock(0) {
			switch d.Val() {
			case "submodule":
				submodule := Submodule{}
				if !d.Args(&submodule.Path) {
					return d.ArgErr()
				}
				
				// Optional submodule URL
				remainingArgs := d.RemainingArgs()
				if len(remainingArgs) > 0 {
					submodule.URL = remainingArgs[0]
				}
				
				m.Submodules = append(m.Submodules, submodule)
			default:
				return d.Errf("unrecognized subdirective '%s'", d.Val())
			}
		}
	}

	return nil
}

func (m *GoPackage) Provision(ctx caddy.Context) error {
	if m.Vcs == "" {
		m.Vcs = "git"
	}

	if m.Template == nil {
		tpl, err := template.New("Package").Parse(DefaultTemplate)
		if err != nil {
			return fmt.Errorf("parsing default gopkg template: %v", err)
		}
		m.Template = tpl
	}

	return nil
}

func (m GoPackage) ServeHTTP(w http.ResponseWriter, r *http.Request, next caddyhttp.Handler) error {
	// Determine the best match for the request path
	targetPath := m.Path
	targetURL := m.URL
	
	// Find the best (longest) matching submodule
	bestMatch := ""
	bestURL := ""
	for _, submodule := range m.Submodules {
		submodulePath := m.Path + submodule.Path
		if (r.URL.Path == submodulePath || 
		    r.URL.Path == submodulePath+"/" ||
		    strings.HasPrefix(r.URL.Path, submodulePath+"/")) &&
		   len(submodulePath) > len(bestMatch) {
			bestMatch = submodulePath
			if submodule.URL != "" {
				bestURL = submodule.URL
			}
		}
	}
	
	// Use best match if found
	if bestMatch != "" {
		targetPath = bestMatch
		if bestURL != "" {
			targetURL = bestURL
		}
	}

	// If go-get is not present, it's most likely a browser request. So let's redirect.
	if r.FormValue("go-get") != "1" {
		http.Redirect(w, r, targetURL, http.StatusTemporaryRedirect)
		return nil
	}

	err := m.Template.Execute(w, struct {
		Host string
		Path string
		Vcs  string
		URL  string
	}{r.Host, targetPath, m.Vcs, targetURL})

	if err != nil {
		return caddyhttp.Error(http.StatusInternalServerError, err)
	}

	w.Header().Set("Content-Type", "text/html")
	return nil
}

// Interface guards
var (
	_ caddy.Provisioner           = (*GoPackage)(nil)
	_ caddyhttp.MiddlewareHandler = (*GoPackage)(nil)
	_ caddyfile.Unmarshaler       = (*GoPackage)(nil)
)
