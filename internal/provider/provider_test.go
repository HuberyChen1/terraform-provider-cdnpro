package provider

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

// Acceptance tests run the real Terraform CLI against the in-memory mock API.
// They need a terraform binary: set TF_ACC_TERRAFORM_PATH or put terraform on
// PATH. Otherwise they are skipped.

const testPrivateKeyPEM = "-----BEGIN PRIVATE KEY-----\nMIIEvQIBADANBgkqhkiG9w0BAQEFAASCBKcwggSjAgEAAoIBAQC7\n-----END PRIVATE KEY-----"
const testCertificatePEM = "-----BEGIN CERTIFICATE-----\nMIIDdzCCAl+gAwIBAgIEbGV4YTANBgkqhkiG9w0BAQsFADBi\n-----END CERTIFICATE-----"

func findTerraform(t *testing.T) string {
	t.Helper()
	if p := os.Getenv("TF_ACC_TERRAFORM_PATH"); p != "" {
		return p
	}
	if p, err := exec.LookPath("terraform"); err == nil {
		return p
	}
	home, _ := os.UserHomeDir()
	for _, candidate := range []string{
		filepath.Join(home, "terraform-cli", "terraform.exe"),
		filepath.Join(home, "terraform-cli", "terraform"),
	} {
		if _, err := os.Stat(candidate); err == nil {
			return candidate
		}
	}
	t.Skip("terraform CLI not found; set TF_ACC_TERRAFORM_PATH to run acceptance tests")
	return ""
}

func testAccSetup(t *testing.T) *mockAPI {
	t.Helper()
	tf := findTerraform(t)
	t.Setenv("TF_ACC", "1")
	t.Setenv("TF_ACC_TERRAFORM_PATH", tf)
	t.Setenv("CHECKPOINT_DISABLE", "1")
	m := newMockAPI()
	t.Cleanup(m.Close)
	return m
}

func protoV6Factories() map[string]func() (tfprotov6.ProviderServer, error) {
	return map[string]func() (tfprotov6.ProviderServer, error){
		"cdnpro": providerserver.NewProtocol6WithError(New("test")()),
	}
}

func providerHCL(m *mockAPI) string {
	return fmt.Sprintf(`
provider "cdnpro" {
  server   = %q
  api_user = %q
  api_key  = %q
  polling {
    validation_interval    = "20ms"
    deployment_interval    = "20ms"
    edge_hostname_interval = "20ms"
    purge_interval         = "20ms"
  }
}
`, m.server.URL, m.user, m.key)
}

func propertyHCL(name, hostnames, edgeLogic string) string {
	return fmt.Sprintf(`
resource "cdnpro_property" "main" {
  name           = %q
  description    = "acceptance test"
  hostnames      = [%s]
  edge_logic     = %q
  syntax_version = 1

  origins = [
    {
      name               = "primary"
      servers            = ["origin.example.com:80,443"]
      supported_protocol = "https"
      host_header        = "origin.example.com"
    }
  ]

  tls_min_version        = "1.2"
  redirect_http_to_https = true
}
`, name, hostnames, edgeLogic)
}

// checkPropertyVersionCount asserts how many versions the mock holds for the
// property in state.
func checkPropertyVersionCount(m *mockAPI, want int) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		rs, ok := s.RootModule().Resources["cdnpro_property.main"]
		if !ok {
			return fmt.Errorf("cdnpro_property.main not in state")
		}
		got := m.propertyVersionCount(rs.Primary.ID)
		if got != want {
			return fmt.Errorf("property %s has %d versions, want %d", rs.Primary.ID, got, want)
		}
		return nil
	}
}

func checkCounts(m *mockAPI, deployments, validations int) resource.TestCheckFunc {
	return func(*terraform.State) error {
		d, v := m.counts()
		if d != deployments || v != validations {
			return fmt.Errorf("mock saw %d deployment and %d validation POSTs, want %d and %d", d, v, deployments, validations)
		}
		return nil
	}
}

func TestAccProperty(t *testing.T) {
	m := testAccSetup(t)
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6Factories(),
		CheckDestroy: func(*terraform.State) error {
			if ids := m.propertyIDs(); len(ids) != 0 {
				return fmt.Errorf("properties left behind: %v", ids)
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				Config: providerHCL(m) + propertyHCL("acc-site", `"www.example.com"`, "location / { origin_pass primary; }"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestMatchResourceAttr("cdnpro_property.main", "id", regexp.MustCompile(`^prop\d+$`)),
					resource.TestCheckResourceAttr("cdnpro_property.main", "latest_version", "1"),
					resource.TestCheckResourceAttr("cdnpro_property.main", "origins.0.direct_connection", "auto"),
					resource.TestCheckResourceAttr("cdnpro_property.main", "origins.0.peer_failure_timeout.failure_threshold", "6"),
					resource.TestCheckResourceAttr("cdnpro_property.main", "redirect_http_to_https", "true"),
					resource.TestCheckResourceAttr("cdnpro_property.main", "tls_max_version", "1.3"),
					resource.TestCheckNoResourceAttr("cdnpro_property.main", "version_in_staging"),
					checkPropertyVersionCount(m, 1),
				),
			},
			{
				// Version-level change on a never-deployed version: PATCH in place, still version 1.
				Config: providerHCL(m) + propertyHCL("acc-site", `"www.example.com", "example.com"`, "location / { origin_pass primary; }"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("cdnpro_property.main", "hostnames.#", "2"),
					resource.TestCheckResourceAttr("cdnpro_property.main", "latest_version", "1"),
					checkPropertyVersionCount(m, 1),
				),
			},
			{
				// Metadata-only change never touches versions.
				Config: providerHCL(m) + propertyHCL("acc-site-renamed", `"www.example.com", "example.com"`, "location / { origin_pass primary; }"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("cdnpro_property.main", "name", "acc-site-renamed"),
					resource.TestCheckResourceAttr("cdnpro_property.main", "latest_version", "1"),
					checkPropertyVersionCount(m, 1),
				),
			},
			{
				ResourceName:      "cdnpro_property.main",
				ImportState:       true,
				ImportStateVerify: true,
			},
		},
	})
}

func certificateHCL(comments, privateKey, certificate string, withIdentification bool) string {
	ident := ""
	if withIdentification {
		ident = `
  identification_info = {
    country                   = "US"
    common_name               = "dev.example.com"
    subject_alternative_names = ["dev.example.com"]
  }`
	}
	return fmt.Sprintf(`
resource "cdnpro_certificate" "test" {
  name        = "acc-cert"
  auto_renew  = "off"
  comments    = %q
  private_key = %q
  certificate = %q%s
}
`, comments, privateKey, certificate, ident)
}

func TestAccCertificate(t *testing.T) {
	m := testAccSetup(t)
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6Factories(),
		Steps: []resource.TestStep{
			{
				Config: providerHCL(m) + certificateHCL("v1", "RSA2048", "GENERATE", true),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestMatchResourceAttr("cdnpro_certificate.test", "id", regexp.MustCompile(`^cert\d+$`)),
					resource.TestCheckResourceAttr("cdnpro_certificate.test", "latest_version", "1"),
					resource.TestCheckResourceAttr("cdnpro_certificate.test", "auto_renew", "off"),
					resource.TestCheckResourceAttr("cdnpro_certificate.test", "algorithm", "RSA"),
					resource.TestCheckNoResourceAttr("cdnpro_certificate.test", "private_key"),
					resource.TestCheckNoResourceAttr("cdnpro_certificate.test", "certificate"),
				),
			},
			{
				// A comment change alone mints a new version.
				Config: providerHCL(m) + certificateHCL("v2", "RSA2048", "GENERATE", true),
				Check:  resource.TestCheckResourceAttr("cdnpro_certificate.test", "latest_version", "2"),
			},
			{
				// Changing write-only material is detected through the private hashes.
				Config: providerHCL(m) + certificateHCL("v2", testPrivateKeyPEM, testCertificatePEM, true),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("cdnpro_certificate.test", "latest_version", "3"),
					func(*terraform.State) error {
						m.mu.Lock()
						defer m.mu.Unlock()
						if len(m.decryptedKeys) != 1 || m.decryptedKeys[0] != testPrivateKeyPEM {
							return fmt.Errorf("mock did not decrypt the uploaded key correctly: %q", m.decryptedKeys)
						}
						return nil
					},
				),
			},
		},
	})
}

func edgeHostnameHCL(weight int, description string) string {
	return fmt.Sprintf(`
resource "cdnpro_edge_hostname" "main" {
  edge_hostname = "acc.edge.example.com"
  description   = %q
  client_zones = [
    {
      region = "all"
      isp    = "all"
      weight = %d
      action = {
        type = "deliver"
        by   = ["standard", "premium"]
      }
    }
  ]
}
`, description, weight)
}

func TestAccEdgeHostname(t *testing.T) {
	m := testAccSetup(t)
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6Factories(),
		CheckDestroy: func(*terraform.State) error {
			m.mu.Lock()
			defer m.mu.Unlock()
			if len(m.edgeHosts) != 0 {
				return fmt.Errorf("edge hostnames left behind: %d", len(m.edgeHosts))
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				Config: providerHCL(m) + edgeHostnameHCL(100, "first"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("cdnpro_edge_hostname.main", "has_beian", "false"),
					resource.TestCheckResourceAttr("cdnpro_edge_hostname.main", "history.#", "1"),
					resource.TestCheckResourceAttr("cdnpro_edge_hostname.main", "history.0.operation", "creation"),
					resource.TestCheckResourceAttr("cdnpro_edge_hostname.main", "history.0.status", "succeeded"),
					resource.TestCheckResourceAttr("cdnpro_edge_hostname.main", "client_zones.0.action.enable_ipv6", "true"),
					resource.TestCheckResourceAttr("cdnpro_edge_hostname.main", "client_zones.0.action.by.#", "2"),
				),
			},
			{
				Config: providerHCL(m) + edgeHostnameHCL(50, "second"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("cdnpro_edge_hostname.main", "client_zones.0.weight", "50"),
					resource.TestCheckResourceAttr("cdnpro_edge_hostname.main", "history.#", "2"),
					resource.TestCheckResourceAttr("cdnpro_edge_hostname.main", "history.0.operation", "update"),
					resource.TestCheckResourceAttr("cdnpro_edge_hostname.main", "history.0.status", "succeeded"),
				),
			},
			{
				ResourceName:                         "cdnpro_edge_hostname.main",
				ImportState:                          true,
				ImportStateId:                        "acc.edge.example.com",
				ImportStateVerify:                    true,
				ImportStateVerifyIdentifierAttribute: "edge_hostname",
				ImportStateVerifyIgnore:              []string{"force_delete"},
			},
		},
	})
}

func deploymentLoopHCL(edgeLogic string) string {
	return propertyHCL("acc-deploy", `"www.example.com"`, edgeLogic) +
		certificateHCL("deploy", "RSA2048", "GENERATE", true) + `
resource "cdnpro_deployment" "staging" {
  target = "staging"
  actions = [
    {
      action      = "deploy_property"
      property_id = cdnpro_property.main.id
      version     = cdnpro_property.main.latest_version
    },
    {
      action         = "deploy_cert"
      certificate_id = cdnpro_certificate.test.id
      version        = cdnpro_certificate.test.latest_version
    },
  ]
}
`
}

func TestAccDeploymentLoop(t *testing.T) {
	m := testAccSetup(t)
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6Factories(),
		CheckDestroy: func(*terraform.State) error {
			m.mu.Lock()
			defer m.mu.Unlock()
			if len(m.properties) != 0 || len(m.certificates) != 0 {
				return fmt.Errorf("resources left behind: %d properties, %d certificates", len(m.properties), len(m.certificates))
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				Config: providerHCL(m) + deploymentLoopHCL("location / { origin_pass primary; }"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("cdnpro_deployment.staging", "status", "succeeded"),
					resource.TestMatchResourceAttr("cdnpro_deployment.staging", "id", regexp.MustCompile(`^dep\d+$`)),
					resource.TestCheckResourceAttr("cdnpro_deployment.staging", "actions.0.version", "1"),
					checkCounts(m, 1, 1),
				),
			},
			{
				// The deployed version is frozen, so an Edge Logic change mints version 2
				// and the deployment follows latest_version.
				Config: providerHCL(m) + deploymentLoopHCL("location / { origin_pass primary; add_header X-Acc 1; }"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("cdnpro_property.main", "latest_version", "2"),
					resource.TestCheckResourceAttr("cdnpro_deployment.staging", "actions.0.version", "2"),
					resource.TestCheckResourceAttr("cdnpro_deployment.staging", "actions.1.version", "1"),
					checkPropertyVersionCount(m, 2),
					checkCounts(m, 2, 2),
					checkDeployedVersion(m, "cdnpro_property.main", "staging", 2),
				),
			},
			{
				// Same configuration again: the refreshed property sees the new
				// deployment and nothing is re-submitted.
				Config: providerHCL(m) + deploymentLoopHCL("location / { origin_pass primary; add_header X-Acc 1; }"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("cdnpro_property.main", "version_in_staging", "2"),
					checkCounts(m, 2, 2),
				),
			},
		},
	})
}

// checkDeployedVersion asserts the mock's deployment pointer for the property in state.
func checkDeployedVersion(m *mockAPI, resourceName, target string, want int64) resource.TestCheckFunc {
	return func(s *terraform.State) error {
		rs, ok := s.RootModule().Resources[resourceName]
		if !ok {
			return fmt.Errorf("%s not in state", resourceName)
		}
		m.mu.Lock()
		defer m.mu.Unlock()
		p, ok := m.properties[rs.Primary.ID]
		if !ok {
			return fmt.Errorf("property %s missing from mock", rs.Primary.ID)
		}
		got := p.inStaging
		if target == "production" {
			got = p.inProduction
		}
		if got == nil || *got != want {
			return fmt.Errorf("property %s deployed to %s: got %v, want %d", rs.Primary.ID, target, got, want)
		}
		return nil
	}
}

func TestAccDeploymentAdopt(t *testing.T) {
	m := testAccSetup(t)
	id := m.seedDeployedProperty("adopted", "staging")
	config := providerHCL(m) + fmt.Sprintf(`
resource "cdnpro_deployment" "adopt" {
  target = "staging"
  actions = [
    {
      action      = "deploy_property"
      property_id = %q
      version     = 1
    },
  ]
}
`, id)
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6Factories(),
		Steps: []resource.TestStep{
			{
				Config: config,
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestMatchResourceAttr("cdnpro_deployment.adopt", "id", regexp.MustCompile(`^adopted:staging:`)),
					resource.TestCheckResourceAttr("cdnpro_deployment.adopt", "task_id", ""),
					checkCounts(m, 0, 0),
				),
			},
		},
	})
	// Destroying an adopted deployment undeploys, like any deploy_* resource.
	d, _ := m.counts()
	if d != 1 {
		t.Fatalf("expected exactly one removal task on destroy, mock saw %d deployment POSTs", d)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if p := m.properties[id]; p.inStaging != nil {
		t.Fatalf("property still deployed to staging after destroy")
	}
	if !strings.Contains(strings.Join(m.requests, "\n"), "POST /cdn/deploymentTasks") {
		t.Fatalf("no deployment task was posted")
	}
}
