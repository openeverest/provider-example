package provider

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"
)

// discoveryServer serves the legacy and named API group lists discovery reads.
func discoveryServer(t *testing.T, groups ...string) *rest.Config {
	t.Helper()

	list := metav1.APIGroupList{TypeMeta: metav1.TypeMeta{Kind: "APIGroupList", APIVersion: "v1"}}
	for _, name := range groups {
		version := metav1.GroupVersionForDiscovery{GroupVersion: name + "/v1", Version: "v1"}
		list.Groups = append(list.Groups, metav1.APIGroup{
			Name: name, Versions: []metav1.GroupVersionForDiscovery{version}, PreferredVersion: version,
		})
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api":
			_ = json.NewEncoder(w).Encode(metav1.APIVersions{
				TypeMeta: metav1.TypeMeta{Kind: "APIVersions"}, Versions: []string{"v1"},
			})
		case "/apis":
			_ = json.NewEncoder(w).Encode(list)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	return &rest.Config{Host: server.URL}
}

func TestDetectOpenShift(t *testing.T) {
	t.Parallel()

	openShift, err := DetectOpenShift(discoveryServer(t, "apps", "security.openshift.io"))
	require.NoError(t, err)
	assert.True(t, openShift)

	openShift, err = DetectOpenShift(discoveryServer(t, "apps", "core.openeverest.io"))
	require.NoError(t, err)
	assert.False(t, openShift)
}
