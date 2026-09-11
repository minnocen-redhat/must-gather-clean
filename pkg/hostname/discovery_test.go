package hostname

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDiscoverResourceHostnames(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "resources.yaml"), []byte(`apiVersion: route.openshift.io/v1
kind: Route
spec:
  host: Console.Apps.Example.com.
status:
  ingress:
    - host: status.apps.example.com
---
apiVersion: networking.k8s.io/v1
kind: Ingress
spec:
  rules:
    - host: ingress.apps.example.com
  tls:
    - hosts:
        - tls.apps.example.com
`), 0600))

	hostnames, err := Discover(root)
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{
		"console.apps.example.com",
		"status.apps.example.com",
		"ingress.apps.example.com",
		"tls.apps.example.com",
	}, hostnames)
}

func TestDiscoverIgnoresUnstructuredLogs(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "application.log"), []byte("host=only-in-log.example.com\n"), 0600))

	hostnames, err := Discover(root)
	require.NoError(t, err)
	assert.Empty(t, hostnames)
}

func TestDiscoverExtractsURLHosts(t *testing.T) {
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "infrastructure.yaml"), []byte(`apiVersion: config.openshift.io/v1
kind: Infrastructure
status:
  apiServerURL: https://api.example.com:6443
  consoleURL: https://console.example.com/
`), 0600))

	hostnames, err := Discover(root)
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"api.example.com", "console.example.com"}, hostnames)
}

func TestDiscoverExtractsHostnamesFromTarGzResources(t *testing.T) {
	root := t.TempDir()
	archivePath := filepath.Join(root, "resources.tar.gz")
	var archive bytes.Buffer
	gzipWriter := gzip.NewWriter(&archive)
	tarWriter := tar.NewWriter(gzipWriter)
	resource := []byte(`apiVersion: route.openshift.io/v1
kind: Route
spec:
  host: archived.apps.example.com
`)
	require.NoError(t, tarWriter.WriteHeader(&tar.Header{Name: "route.yaml", Mode: 0600, Size: int64(len(resource))}))
	_, err := tarWriter.Write(resource)
	require.NoError(t, err)
	require.NoError(t, tarWriter.Close())
	require.NoError(t, gzipWriter.Close())
	require.NoError(t, os.WriteFile(archivePath, archive.Bytes(), 0600))

	hostnames, err := Discover(root)
	require.NoError(t, err)
	assert.Equal(t, []string{"archived.apps.example.com"}, hostnames)
}
