// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: BUSL-1.1

package xds

import (
	"bytes"
	"path/filepath"
	"sort"
	"testing"
	"text/template"
	"time"

	"github.com/hashicorp/consul/types"

	envoy_cluster_v3 "github.com/envoyproxy/go-control-plane/envoy/config/cluster/v3"
	envoy_core_v3 "github.com/envoyproxy/go-control-plane/envoy/config/core/v3"
	envoy_tls_v3 "github.com/envoyproxy/go-control-plane/envoy/extensions/transport_sockets/tls/v3"
	testinf "github.com/mitchellh/go-testing-interface"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/wrapperspb"

	"github.com/hashicorp/consul/agent/proxycfg"
	"github.com/hashicorp/consul/agent/structs"
	"github.com/hashicorp/consul/agent/xds/proxystateconverter"
	"github.com/hashicorp/consul/agent/xds/response"
	"github.com/hashicorp/consul/agent/xds/testcommon"
	"github.com/hashicorp/consul/agent/xdsv2"
	"github.com/hashicorp/consul/envoyextensions/xdscommon"
	"github.com/hashicorp/consul/sdk/testutil"
)

type mockCfgFetcher struct {
	addressLan string
}

func (s *mockCfgFetcher) AdvertiseAddrLAN() string {
	return s.addressLan
}

type clusterTestCase struct {
	name               string
	create             func(t testinf.T) *proxycfg.ConfigSnapshot
	overrideGoldenName string
	alsoRunTestForV2   bool
}

func uint32ptr(i uint32) *uint32 {
	return &i
}

func durationPtr(d time.Duration) *time.Duration {
	return &d
}

func TestClustersFromSnapshot(t *testing.T) {
	// TODO: we should move all of these to TestAllResourcesFromSnapshot
	// eventually to test all of the xDS types at once with the same input,
	// just as it would be triggered by our xDS server.
	if testing.Short() {
		t.Skip("too slow for testing.Short")
	}

	tests := []clusterTestCase{
		{
			name: "connect-proxy-with-tls-outgoing-min-version-auto",
			create: func(t testinf.T) *proxycfg.ConfigSnapshot {
				return proxycfg.TestConfigSnapshot(t, nil, []proxycfg.UpdateEvent{
					{
						CorrelationID: "mesh",
						Result: &structs.ConfigEntryResponse{
							Entry: &structs.MeshConfigEntry{
								TLS: &structs.MeshTLSConfig{
									Outgoing: &structs.MeshDirectionalTLSConfig{
										TLSMinVersion: types.TLSVersionAuto,
									},
								},
							},
						},
					},
				})
			},
			alsoRunTestForV2: true,
		},
		{
			name: "connect-proxy-with-tls-outgoing-min-version",
			create: func(t testinf.T) *proxycfg.ConfigSnapshot {
				return proxycfg.TestConfigSnapshot(t, nil, []proxycfg.UpdateEvent{
					{
						CorrelationID: "mesh",
						Result: &structs.ConfigEntryResponse{
							Entry: &structs.MeshConfigEntry{
								TLS: &structs.MeshTLSConfig{
									Outgoing: &structs.MeshDirectionalTLSConfig{
										TLSMinVersion: types.TLSv1_3,
									},
								},
							},
						},
					},
				})
			},
			alsoRunTestForV2: true,
		},
		{
			name: "connect-proxy-with-tls-outgoing-max-version",
			create: func(t testinf.T) *proxycfg.ConfigSnapshot {
				return proxycfg.TestConfigSnapshot(t, nil, []proxycfg.UpdateEvent{
					{
						CorrelationID: "mesh",
						Result: &structs.ConfigEntryResponse{
							Entry: &structs.MeshConfigEntry{
								TLS: &structs.MeshTLSConfig{
									Outgoing: &structs.MeshDirectionalTLSConfig{
										TLSMaxVersion: types.TLSv1_2,
									},
								},
							},
						},
					},
				})
			},
			alsoRunTestForV2: true,
		},
		{
			name: "connect-proxy-with-tls-outgoing-cipher-suites",
			create: func(t testinf.T) *proxycfg.ConfigSnapshot {
				return proxycfg.TestConfigSnapshot(t, nil, []proxycfg.UpdateEvent{
					{
						CorrelationID: "mesh",
						Result: &structs.ConfigEntryResponse{
							Entry: &structs.MeshConfigEntry{
								TLS: &structs.MeshTLSConfig{
									Outgoing: &structs.MeshDirectionalTLSConfig{
										CipherSuites: []types.TLSCipherSuite{
											types.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,
											types.TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305_SHA256,
										},
									},
								},
							},
						},
					},
				})
			},
			alsoRunTestForV2: true,
		},
		{
			name: "mesh-gateway",
			create: func(t testinf.T) *proxycfg.ConfigSnapshot {
				return proxycfg.TestConfigSnapshotMeshGateway(t, "default", nil, nil)
			},
			// TODO(proxystate): mesh gateway will come at a later time
			alsoRunTestForV2: false,
		},
		{
			name: "mesh-gateway-using-federation-states",
			create: func(t testinf.T) *proxycfg.ConfigSnapshot {
				return proxycfg.TestConfigSnapshotMeshGateway(t, "federation-states", nil, nil)
			},
			// TODO(proxystate): mesh gateway will come at a later time
			alsoRunTestForV2: false,
		},
		{
			name: "mesh-gateway-using-federation-control-plane",
			create: func(t testinf.T) *proxycfg.ConfigSnapshot {
				return proxycfg.TestConfigSnapshotMeshGateway(t, "mesh-gateway-federation", nil, nil)
			},
			// TODO(proxystate): mesh gateway will come at a later time
			alsoRunTestForV2: false,
		},
		{
			name: "mesh-gateway-no-services",
			create: func(t testinf.T) *proxycfg.ConfigSnapshot {
				return proxycfg.TestConfigSnapshotMeshGateway(t, "no-services", nil, nil)
			},
			// TODO(proxystate): mesh gateway will come at a later time
			alsoRunTestForV2: false,
		},
		{
			name: "mesh-gateway-service-subsets",
			create: func(t testinf.T) *proxycfg.ConfigSnapshot {
				return proxycfg.TestConfigSnapshotMeshGateway(t, "service-subsets", nil, nil)
			},
			// TODO(proxystate): mesh gateway will come at a later time
			alsoRunTestForV2: false,
		},
		{
			name: "mesh-gateway-ignore-extra-resolvers",
			create: func(t testinf.T) *proxycfg.ConfigSnapshot {
				return proxycfg.TestConfigSnapshotMeshGateway(t, "ignore-extra-resolvers", nil, nil)
			},
			// TODO(proxystate): mesh gateway will come at a later time
			alsoRunTestForV2: false,
		},
		{
			name: "mesh-gateway-service-timeouts",
			create: func(t testinf.T) *proxycfg.ConfigSnapshot {
				return proxycfg.TestConfigSnapshotMeshGateway(t, "service-timeouts", nil, nil)
			},
			// TODO(proxystate): mesh gateway will come at a later time
			alsoRunTestForV2: false,
		},
		{
			name: "mesh-gateway-non-hash-lb-injected",
			create: func(t testinf.T) *proxycfg.ConfigSnapshot {
				return proxycfg.TestConfigSnapshotMeshGateway(t, "non-hash-lb-injected", nil, nil)
			},
			// TODO(proxystate): mesh gateway will come at a later time
			alsoRunTestForV2: false,
		},
		{
			name: "mesh-gateway-hash-lb-ignored",
			create: func(t testinf.T) *proxycfg.ConfigSnapshot {
				return proxycfg.TestConfigSnapshotMeshGateway(t, "hash-lb-ignored", nil, nil)
			},
			// TODO(proxystate): mesh gateway will come at a later time
			alsoRunTestForV2: false,
		},
		{
			name: "mesh-gateway-tcp-keepalives",
			create: func(t testinf.T) *proxycfg.ConfigSnapshot {
				return proxycfg.TestConfigSnapshotMeshGateway(t, "default", func(ns *structs.NodeService) {
					ns.Proxy.Config["envoy_gateway_remote_tcp_enable_keepalive"] = true
					ns.Proxy.Config["envoy_gateway_remote_tcp_keepalive_time"] = 120
					ns.Proxy.Config["envoy_gateway_remote_tcp_keepalive_interval"] = 60
					ns.Proxy.Config["envoy_gateway_remote_tcp_keepalive_probes"] = 7
				}, nil)
			},
			// TODO(proxystate): mesh gateway will come at a later time
			alsoRunTestForV2: false,
		},
		{
			name:             "transparent-proxy-catalog-destinations-only",
			create:           proxycfg.TestConfigSnapshotTransparentProxyCatalogDestinationsOnly,
			alsoRunTestForV2: true,
		},
		{
			name:             "transparent-proxy-dial-instances-directly",
			create:           proxycfg.TestConfigSnapshotTransparentProxyDialDirectly,
			alsoRunTestForV2: true,
		},
	}

	latestEnvoyVersion := xdscommon.EnvoyVersions[0]
	for _, envoyVersion := range xdscommon.EnvoyVersions {
		sf, err := xdscommon.DetermineSupportedProxyFeaturesFromString(envoyVersion)
		require.NoError(t, err)
		t.Run("envoy-"+envoyVersion, func(t *testing.T) {
			for _, tt := range tests {
				t.Run(tt.name, func(t *testing.T) {
					// Sanity check default with no overrides first
					snap := tt.create(t)

					// We need to replace the TLS certs with deterministic ones to make golden
					// files workable. Note we don't update these otherwise they'd change
					// golder files for every test case and so not be any use!
					testcommon.SetupTLSRootsAndLeaf(t, snap)

					// Need server just for logger dependency
					g := NewResourceGenerator(testutil.Logger(t), nil, false)
					g.ProxyFeatures = sf

					clusters, err := g.clustersFromSnapshot(snap)
					require.NoError(t, err)

					sort.Slice(clusters, func(i, j int) bool {
						return clusters[i].(*envoy_cluster_v3.Cluster).Name < clusters[j].(*envoy_cluster_v3.Cluster).Name
					})

					r, err := response.CreateResponse(xdscommon.ClusterType, "00000001", "00000001", clusters)
					require.NoError(t, err)

					t.Run("current-xdsv1", func(t *testing.T) {
						gotJSON := protoToJSON(t, r)

						gName := tt.name
						if tt.overrideGoldenName != "" {
							gName = tt.overrideGoldenName
						}

						require.JSONEq(t, goldenEnvoy(t, filepath.Join("clusters", gName), envoyVersion, latestEnvoyVersion, gotJSON), gotJSON)
					})

					if tt.alsoRunTestForV2 {
						generator := xdsv2.NewResourceGenerator(testutil.Logger(t))

						converter := proxystateconverter.NewConverter(testutil.Logger(t), &mockCfgFetcher{addressLan: "10.10.10.10"})
						proxyState, err := converter.ProxyStateFromSnapshot(snap)
						require.NoError(t, err)

						res, err := generator.AllResourcesFromIR(proxyState)
						require.NoError(t, err)

						clusters = res[xdscommon.ClusterType]

						// The order of clusters returned via CDS isn't relevant, so it's safe
						// to sort these for the purposes of test comparisons.
						sort.Slice(clusters, func(i, j int) bool {
							return clusters[i].(*envoy_cluster_v3.Cluster).Name < clusters[j].(*envoy_cluster_v3.Cluster).Name
						})

						r, err := response.CreateResponse(xdscommon.ClusterType, "00000001", "00000001", clusters)
						require.NoError(t, err)

						t.Run("current-xdsv2", func(t *testing.T) {
							gotJSON := protoToJSON(t, r)

							gName := tt.name
							if tt.overrideGoldenName != "" {
								gName = tt.overrideGoldenName
							}

							expectedJSON := goldenEnvoy(t, filepath.Join("clusters", gName), envoyVersion, latestEnvoyVersion, gotJSON)
							require.JSONEq(t, expectedJSON, gotJSON)
						})
					}
				})
			}
		})
	}
}

type customClusterJSONOptions struct {
	Name       string
	TLSContext string
}

var customAppClusterJSONTpl = `{
	"@type": "type.googleapis.com/envoy.config.cluster.v3.Cluster",
	{{ if .TLSContext -}}
	"transport_socket": {
		"name": "tls",
		"typed_config": {
			"@type": "type.googleapis.com/envoy.extensions.transport_sockets.tls.v3.UpstreamTlsContext",
			{{ .TLSContext }}
		}
	},
	{{- end }}
	"name": "{{ .Name }}",
	"connectTimeout": "15s",
	"loadAssignment": {
		"clusterName": "{{ .Name }}",
		"endpoints": [
			{
				"lbEndpoints": [
					{
						"endpoint": {
							"address": {
								"socketAddress": {
									"address": "127.0.0.1",
									"portValue": 8080
								}
							}
						}
					}
				]
			}
		]
	}
}`

var customAppClusterJSONTemplate = template.Must(template.New("").Parse(customAppClusterJSONTpl))

func customAppClusterJSON(t testinf.T, opts customClusterJSONOptions) string {
	t.Helper()
	var buf bytes.Buffer
	err := customAppClusterJSONTemplate.Execute(&buf, opts)
	require.NoError(t, err)
	return buf.String()
}

var customClusterJSONTpl = `{
	"@type": "type.googleapis.com/envoy.config.cluster.v3.Cluster",
	"name": "{{ .Name }}",
	"connectTimeout": "15s",
	"loadAssignment": {
		"clusterName": "{{ .Name }}",
		"endpoints": [
			{
				"lbEndpoints": [
					{
						"endpoint": {
							"address": {
								"socketAddress": {
									"address": "1.2.3.4",
									"portValue": 8443
								}
							}
						}
					}
				]
			}
		]
	}
}`

var customClusterJSONTemplate = template.Must(template.New("").Parse(customClusterJSONTpl))

func customClusterJSON(t testinf.T, opts customClusterJSONOptions) string {
	t.Helper()
	var buf bytes.Buffer
	err := customClusterJSONTemplate.Execute(&buf, opts)
	require.NoError(t, err)
	return buf.String()
}

func TestEnvoyLBConfig_InjectToCluster(t *testing.T) {
	var tests = []struct {
		name     string
		lb       *structs.LoadBalancer
		expected *envoy_cluster_v3.Cluster
	}{
		{
			name: "skip empty",
			lb: &structs.LoadBalancer{
				Policy: "",
			},
			expected: &envoy_cluster_v3.Cluster{},
		},
		{
			name: "round robin",
			lb: &structs.LoadBalancer{
				Policy: structs.LBPolicyRoundRobin,
			},
			expected: &envoy_cluster_v3.Cluster{LbPolicy: envoy_cluster_v3.Cluster_ROUND_ROBIN},
		},
		{
			name: "random",
			lb: &structs.LoadBalancer{
				Policy: structs.LBPolicyRandom,
			},
			expected: &envoy_cluster_v3.Cluster{LbPolicy: envoy_cluster_v3.Cluster_RANDOM},
		},
		{
			name: "maglev",
			lb: &structs.LoadBalancer{
				Policy: structs.LBPolicyMaglev,
			},
			expected: &envoy_cluster_v3.Cluster{LbPolicy: envoy_cluster_v3.Cluster_MAGLEV},
		},
		{
			name: "ring_hash",
			lb: &structs.LoadBalancer{
				Policy: structs.LBPolicyRingHash,
				RingHashConfig: &structs.RingHashConfig{
					MinimumRingSize: 3,
					MaximumRingSize: 7,
				},
			},
			expected: &envoy_cluster_v3.Cluster{
				LbPolicy: envoy_cluster_v3.Cluster_RING_HASH,
				LbConfig: &envoy_cluster_v3.Cluster_RingHashLbConfig_{
					RingHashLbConfig: &envoy_cluster_v3.Cluster_RingHashLbConfig{
						MinimumRingSize: &wrapperspb.UInt64Value{Value: 3},
						MaximumRingSize: &wrapperspb.UInt64Value{Value: 7},
					},
				},
			},
		},
		{
			name: "least_request",
			lb: &structs.LoadBalancer{
				Policy: "least_request",
				LeastRequestConfig: &structs.LeastRequestConfig{
					ChoiceCount: 3,
				},
			},
			expected: &envoy_cluster_v3.Cluster{
				LbPolicy: envoy_cluster_v3.Cluster_LEAST_REQUEST,
				LbConfig: &envoy_cluster_v3.Cluster_LeastRequestLbConfig_{
					LeastRequestLbConfig: &envoy_cluster_v3.Cluster_LeastRequestLbConfig{
						ChoiceCount: &wrapperspb.UInt32Value{Value: 3},
					},
				},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var c envoy_cluster_v3.Cluster
			err := injectLBToCluster(tc.lb, &c)
			require.NoError(t, err)

			require.Equal(t, tc.expected, &c)
		})
	}
}

func TestMakeJWTProviderCluster(t *testing.T) {
	// All tests here depend on golden files located under: agent/xds/testdata/jwt_authn_cluster/*
	tests := map[string]struct {
		provider      *structs.JWTProviderConfigEntry
		expectedError string
	}{
		"remote-jwks-not-configured": {
			provider: &structs.JWTProviderConfigEntry{
				Kind:          "jwt-provider",
				Name:          "okta",
				JSONWebKeySet: &structs.JSONWebKeySet{},
			},
			expectedError: "cannot create JWKS cluster for non remote JWKS. Provider Name: okta",
		},
		"local-jwks-configured": {
			provider: &structs.JWTProviderConfigEntry{
				Kind: "jwt-provider",
				Name: "okta",
				JSONWebKeySet: &structs.JSONWebKeySet{
					Local: &structs.LocalJWKS{
						Filename: "filename",
					},
				},
			},
			expectedError: "cannot create JWKS cluster for non remote JWKS. Provider Name: okta",
		},
		"https-provider-with-hostname-no-port": {
			provider: makeTestProviderWithJWKS("https://example-okta.com/.well-known/jwks.json"),
		},
		"http-provider-with-hostname-no-port": {
			provider: makeTestProviderWithJWKS("http://example-okta.com/.well-known/jwks.json"),
		},
		"https-provider-with-hostname-and-port": {
			provider: makeTestProviderWithJWKS("https://example-okta.com:90/.well-known/jwks.json"),
		},
		"http-provider-with-hostname-and-port": {
			provider: makeTestProviderWithJWKS("http://example-okta.com:90/.well-known/jwks.json"),
		},
		"https-provider-with-ip-no-port": {
			provider: makeTestProviderWithJWKS("https://127.0.0.1"),
		},
		"http-provider-with-ip-no-port": {
			provider: makeTestProviderWithJWKS("http://127.0.0.1"),
		},
		"https-provider-with-ip-and-port": {
			provider: makeTestProviderWithJWKS("https://127.0.0.1:9091"),
		},
		"http-provider-with-ip-and-port": {
			provider: makeTestProviderWithJWKS("http://127.0.0.1:9091"),
		},
	}

	for name, tt := range tests {
		tt := tt
		t.Run(name, func(t *testing.T) {
			cluster, err := makeJWTProviderCluster(tt.provider)
			if tt.expectedError != "" {
				require.Error(t, err, tt.expectedError)
			} else {
				require.NoError(t, err)
				gotJSON := protoToJSON(t, cluster)
				require.JSONEq(t, goldenSimple(t, filepath.Join("jwt_authn_clusters", name), gotJSON), gotJSON)
			}

		})
	}
}

func makeTestProviderWithJWKS(uri string) *structs.JWTProviderConfigEntry {
	return &structs.JWTProviderConfigEntry{
		Kind:   "jwt-provider",
		Name:   "okta",
		Issuer: "test-issuer",
		JSONWebKeySet: &structs.JSONWebKeySet{
			Remote: &structs.RemoteJWKS{
				RequestTimeoutMs:    1000,
				FetchAsynchronously: true,
				URI:                 uri,
				JWKSCluster: &structs.JWKSCluster{
					DiscoveryType:  structs.DiscoveryTypeStatic,
					ConnectTimeout: time.Duration(5) * time.Second,
					TLSCertificates: &structs.JWKSTLSCertificate{
						TrustedCA: &structs.JWKSTLSCertTrustedCA{
							Filename: "mycert.crt",
						},
					},
				},
			},
		},
	}
}

func TestMakeJWKSDiscoveryClusterType(t *testing.T) {
	tests := map[string]struct {
		remoteJWKS          *structs.RemoteJWKS
		expectedClusterType *envoy_cluster_v3.Cluster_Type
	}{
		"nil remote jwks": {
			remoteJWKS:          nil,
			expectedClusterType: &envoy_cluster_v3.Cluster_Type{},
		},
		"nil jwks cluster": {
			remoteJWKS:          &structs.RemoteJWKS{},
			expectedClusterType: &envoy_cluster_v3.Cluster_Type{},
		},
		"jwks cluster defaults to Strict DNS": {
			remoteJWKS: &structs.RemoteJWKS{
				JWKSCluster: &structs.JWKSCluster{},
			},
			expectedClusterType: &envoy_cluster_v3.Cluster_Type{
				Type: envoy_cluster_v3.Cluster_STRICT_DNS,
			},
		},
		"jwks with cluster EDS": {
			remoteJWKS: &structs.RemoteJWKS{
				JWKSCluster: &structs.JWKSCluster{
					DiscoveryType: "EDS",
				},
			},
			expectedClusterType: &envoy_cluster_v3.Cluster_Type{
				Type: envoy_cluster_v3.Cluster_EDS,
			},
		},
		"jwks with static dns": {
			remoteJWKS: &structs.RemoteJWKS{
				JWKSCluster: &structs.JWKSCluster{
					DiscoveryType: "STATIC",
				},
			},
			expectedClusterType: &envoy_cluster_v3.Cluster_Type{
				Type: envoy_cluster_v3.Cluster_STATIC,
			},
		},

		"jwks with original dst": {
			remoteJWKS: &structs.RemoteJWKS{
				JWKSCluster: &structs.JWKSCluster{
					DiscoveryType: "ORIGINAL_DST",
				},
			},
			expectedClusterType: &envoy_cluster_v3.Cluster_Type{
				Type: envoy_cluster_v3.Cluster_ORIGINAL_DST,
			},
		},
		"jwks with strict dns": {
			remoteJWKS: &structs.RemoteJWKS{
				JWKSCluster: &structs.JWKSCluster{
					DiscoveryType: "STRICT_DNS",
				},
			},
			expectedClusterType: &envoy_cluster_v3.Cluster_Type{
				Type: envoy_cluster_v3.Cluster_STRICT_DNS,
			},
		},
		"jwks with logical dns": {
			remoteJWKS: &structs.RemoteJWKS{
				JWKSCluster: &structs.JWKSCluster{
					DiscoveryType: "LOGICAL_DNS",
				},
			},
			expectedClusterType: &envoy_cluster_v3.Cluster_Type{
				Type: envoy_cluster_v3.Cluster_LOGICAL_DNS,
			},
		},
	}

	for name, tt := range tests {
		tt := tt
		t.Run(name, func(t *testing.T) {
			clusterType := makeJWKSDiscoveryClusterType(tt.remoteJWKS)

			require.Equal(t, tt.expectedClusterType, clusterType)
		})
	}
}

func TestParseJWTRemoteURL(t *testing.T) {
	tests := map[string]struct {
		uri            string
		expectedHost   string
		expectedPort   int
		expectedScheme string
		expectError    bool
	}{
		"invalid-url": {
			uri:         ".com",
			expectError: true,
		},
		"https-hostname-no-port": {
			uri:            "https://test.test.com",
			expectedHost:   "test.test.com",
			expectedPort:   443,
			expectedScheme: "https",
		},
		"https-hostname-with-port": {
			uri:            "https://test.test.com:4545",
			expectedHost:   "test.test.com",
			expectedPort:   4545,
			expectedScheme: "https",
		},
		"https-hostname-with-port-and-path": {
			uri:            "https://test.test.com:4545/test",
			expectedHost:   "test.test.com",
			expectedPort:   4545,
			expectedScheme: "https",
		},
		"http-hostname-no-port": {
			uri:            "http://test.test.com",
			expectedHost:   "test.test.com",
			expectedPort:   80,
			expectedScheme: "http",
		},
		"http-hostname-with-port": {
			uri:            "http://test.test.com:4636",
			expectedHost:   "test.test.com",
			expectedPort:   4636,
			expectedScheme: "http",
		},
		"https-ip-no-port": {
			uri:            "https://127.0.0.1",
			expectedHost:   "127.0.0.1",
			expectedPort:   443,
			expectedScheme: "https",
		},
		"https-ip-with-port": {
			uri:            "https://127.0.0.1:3434",
			expectedHost:   "127.0.0.1",
			expectedPort:   3434,
			expectedScheme: "https",
		},
		"http-ip-no-port": {
			uri:            "http://127.0.0.1",
			expectedHost:   "127.0.0.1",
			expectedPort:   80,
			expectedScheme: "http",
		},
		"http-ip-with-port": {
			uri:            "http://127.0.0.1:9190",
			expectedHost:   "127.0.0.1",
			expectedPort:   9190,
			expectedScheme: "http",
		},
		"http-ip-with-port-and-path": {
			uri:            "http://127.0.0.1:9190/some/where",
			expectedHost:   "127.0.0.1",
			expectedPort:   9190,
			expectedScheme: "http",
		},
		"http-ip-no-port-with-path": {
			uri:            "http://127.0.0.1/test/path",
			expectedHost:   "127.0.0.1",
			expectedPort:   80,
			expectedScheme: "http",
		},
	}

	for name, tt := range tests {
		tt := tt
		t.Run(name, func(t *testing.T) {
			host, scheme, port, err := parseJWTRemoteURL(tt.uri)
			if tt.expectError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
				require.Equal(t, host, tt.expectedHost)
				require.Equal(t, scheme, tt.expectedScheme)
				require.Equal(t, port, tt.expectedPort)
			}
		})
	}
}

// UID is just a convenience function to aid in writing tests less verbosely.
func UID(input string) proxycfg.UpstreamID {
	return proxycfg.UpstreamIDFromString(input)
}

func TestMakeJWTCertValidationContext(t *testing.T) {
	tests := map[string]struct {
		jwksCluster *structs.JWKSCluster
		expected    *envoy_tls_v3.CertificateValidationContext
	}{
		"when nil": {
			jwksCluster: nil,
			expected:    &envoy_tls_v3.CertificateValidationContext{},
		},
		"when trustedCA with filename": {
			jwksCluster: &structs.JWKSCluster{
				TLSCertificates: &structs.JWKSTLSCertificate{
					TrustedCA: &structs.JWKSTLSCertTrustedCA{
						Filename: "file.crt",
					},
				},
			},
			expected: &envoy_tls_v3.CertificateValidationContext{
				TrustedCa: &envoy_core_v3.DataSource{
					Specifier: &envoy_core_v3.DataSource_Filename{
						Filename: "file.crt",
					},
				},
			},
		},
		"when trustedCA with environment variable": {
			jwksCluster: &structs.JWKSCluster{
				TLSCertificates: &structs.JWKSTLSCertificate{
					TrustedCA: &structs.JWKSTLSCertTrustedCA{
						EnvironmentVariable: "MY_ENV",
					},
				},
			},
			expected: &envoy_tls_v3.CertificateValidationContext{
				TrustedCa: &envoy_core_v3.DataSource{
					Specifier: &envoy_core_v3.DataSource_EnvironmentVariable{
						EnvironmentVariable: "MY_ENV",
					},
				},
			},
		},
		"when trustedCA with inline string": {
			jwksCluster: &structs.JWKSCluster{
				TLSCertificates: &structs.JWKSTLSCertificate{
					TrustedCA: &structs.JWKSTLSCertTrustedCA{
						InlineString: "<my ca cert>",
					},
				},
			},
			expected: &envoy_tls_v3.CertificateValidationContext{
				TrustedCa: &envoy_core_v3.DataSource{
					Specifier: &envoy_core_v3.DataSource_InlineString{
						InlineString: "<my ca cert>",
					},
				},
			},
		},
		"when trustedCA with inline bytes": {
			jwksCluster: &structs.JWKSCluster{
				TLSCertificates: &structs.JWKSTLSCertificate{
					TrustedCA: &structs.JWKSTLSCertTrustedCA{
						InlineBytes: []byte{1, 2, 3},
					},
				},
			},
			expected: &envoy_tls_v3.CertificateValidationContext{
				TrustedCa: &envoy_core_v3.DataSource{
					Specifier: &envoy_core_v3.DataSource_InlineBytes{
						InlineBytes: []byte{1, 2, 3},
					},
				},
			},
		},
		"when caCertificateProviderInstance": {
			jwksCluster: &structs.JWKSCluster{
				TLSCertificates: &structs.JWKSTLSCertificate{
					CaCertificateProviderInstance: &structs.JWKSTLSCertProviderInstance{
						InstanceName:    "<my-instance-name>",
						CertificateName: "<my-cert>.crt",
					},
				},
			},
			expected: &envoy_tls_v3.CertificateValidationContext{
				CaCertificateProviderInstance: &envoy_tls_v3.CertificateProviderPluginInstance{
					InstanceName:    "<my-instance-name>",
					CertificateName: "<my-cert>.crt",
				},
			},
		},
	}
	for name, tt := range tests {
		tt := tt
		t.Run(name, func(t *testing.T) {
			vc := makeJWTCertValidationContext(tt.jwksCluster)

			require.Equal(t, tt.expected, vc)
		})
	}
}
