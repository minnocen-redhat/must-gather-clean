package hostname

import (
	"bytes"
	"io"
	"io/fs"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"unicode"

	"gopkg.in/yaml.v3"
)

// Discover walks structured Kubernetes/OpenShift resources and returns the
// hostnames found in explicitly supported semantic fields. It deliberately
// does not infer hostnames from arbitrary log text.
func Discover(root string) ([]string, error) {
	seen := map[string]struct{}{}
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
			return nil
		}
		extension := strings.ToLower(filepath.Ext(path))
		if extension != ".yaml" && extension != ".yml" && extension != ".json" {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		if err := discoverDocuments(data, seen); err != nil {
			// A must-gather can contain files with YAML extensions that are not
			// Kubernetes resources. They are outside this detector's scope.
			return nil
		}
		return nil
	})
	if err != nil {
		return nil, err
	}

	result := make([]string, 0, len(seen))
	for value := range seen {
		result = append(result, value)
	}
	slices.Sort(result)
	return result, nil
}

func discoverDocuments(data []byte, seen map[string]struct{}) error {
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	for {
		var document map[string]interface{}
		err := decoder.Decode(&document)
		if err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
		if document == nil {
			continue
		}
		collectResource(document, seen)
	}
}

func collectResource(document map[string]interface{}, seen map[string]struct{}) {
	kind, _ := document["kind"].(string)
	if kind == "" {
		return
	}
	if strings.HasSuffix(kind, "List") {
		if items, ok := document["items"].([]interface{}); ok {
			for _, item := range items {
				if resource, ok := item.(map[string]interface{}); ok {
					collectResource(resource, seen)
				}
			}
		}
		return
	}

	switch kind {
	case "Route":
		collectStringAt(document, seen, "spec", "host")
		collectStringAt(document, seen, "status", "ingress", "host")
	case "Ingress":
		collectStringAt(document, seen, "spec", "rules", "host")
		collectStringAt(document, seen, "spec", "tls", "hosts")
	case "Service":
		collectStringAt(document, seen, "spec", "externalName")
	case "IngressController":
		collectStringAt(document, seen, "spec", "domain")
		collectStringAt(document, seen, "status", "domain")
	case "DNS":
		collectStringAt(document, seen, "spec", "baseDomain")
		collectStringAt(document, seen, "status", "clusterDomain")
	case "Infrastructure":
		collectURLHostAt(document, seen, "status", "apiServerURL")
		collectURLHostAt(document, seen, "status", "apiServerInternalURI")
		collectURLHostAt(document, seen, "status", "oauthServerURL")
		collectURLHostAt(document, seen, "status", "consoleURL")
	case "APIServer":
		collectStringAt(document, seen, "spec", "servingCerts", "namedCertificates", "names")
	}
}

func collectStringAt(value interface{}, seen map[string]struct{}, path ...string) {
	if len(path) == 0 {
		if text, ok := value.(string); ok {
			if normalized, ok := normalize(text); ok {
				seen[normalized] = struct{}{}
			}
			return
		}
		if values, ok := value.([]interface{}); ok {
			for _, item := range values {
				collectStringAt(item, seen)
			}
		}
		return
	}
	walkPath(value, path, func(value interface{}) {
		collectStringAt(value, seen)
	})
}

func collectURLHostAt(value interface{}, seen map[string]struct{}, path ...string) {
	if len(path) == 0 {
		text, ok := value.(string)
		if !ok {
			return
		}
		parsed, err := url.Parse(text)
		if err == nil && parsed.Hostname() != "" {
			if normalized, ok := normalize(parsed.Hostname()); ok {
				seen[normalized] = struct{}{}
			}
		}
		return
	}
	walkPath(value, path, func(value interface{}) {
		collectURLHostAt(value, seen)
	})
}

func walkPath(value interface{}, path []string, visit func(interface{})) {
	if len(path) == 0 {
		visit(value)
		return
	}
	switch current := value.(type) {
	case map[string]interface{}:
		walkPath(current[path[0]], path[1:], visit)
	case []interface{}:
		for _, item := range current {
			walkPath(item, path, visit)
		}
	}
}

func normalize(value string) (string, bool) {
	value = strings.ToLower(strings.TrimSuffix(strings.TrimSpace(value), "."))
	if value == "" || net.ParseIP(value) != nil || strings.ContainsAny(value, "/: \t\r\n") {
		return "", false
	}
	if len(value) > 253 {
		return "", false
	}
	for _, label := range strings.Split(value, ".") {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return "", false
		}
		for _, r := range label {
			if !(unicode.IsLetter(r) || unicode.IsDigit(r) || r == '-') {
				return "", false
			}
		}
	}
	return value, true
}
