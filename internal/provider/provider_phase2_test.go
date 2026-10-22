package provider

import (
	"fmt"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

func secretHCL(name, description, secret string) string {
	desc := ""
	if description != "" {
		desc = fmt.Sprintf("\n  description = %q", description)
	}
	return fmt.Sprintf(`
resource "cdnpro_secret" "auth" {
  name   = %q%s
  secret = %q
}
`, name, desc, secret)
}

func TestAccSecret(t *testing.T) {
	m := testAccSetup(t)
	storedSecret := func(want string) resource.TestCheckFunc {
		return func(s *terraform.State) error {
			rs := s.RootModule().Resources["cdnpro_secret.auth"]
			m.mu.Lock()
			defer m.mu.Unlock()
			sec, ok := m.secrets[rs.Primary.ID]
			if !ok {
				return fmt.Errorf("secret %s missing from mock", rs.Primary.ID)
			}
			if sec.secret != want {
				return fmt.Errorf("mock stores secret %q, want %q", sec.secret, want)
			}
			return nil
		}
	}
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6Factories(),
		CheckDestroy: func(*terraform.State) error {
			m.mu.Lock()
			defer m.mu.Unlock()
			if len(m.secrets) != 0 {
				return fmt.Errorf("secrets left behind: %d", len(m.secrets))
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				Config: providerHCL(m) + secretHCL("origin-auth", "shared token", "s3cr3t-value-1"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestMatchResourceAttr("cdnpro_secret.auth", "id", regexp.MustCompile(`^sec\d+$`)),
					resource.TestCheckResourceAttr("cdnpro_secret.auth", "name", "origin-auth"),
					resource.TestCheckNoResourceAttr("cdnpro_secret.auth", "secret"),
					resource.TestCheckResourceAttr("cdnpro_secret.auth", "usage_in_production.#", "0"),
					storedSecret("s3cr3t-value-1"),
				),
			},
			{
				// Rotating the write-only value is detected via the private hash and patched in place.
				Config: providerHCL(m) + secretHCL("origin-auth", "shared token", "s3cr3t-value-2"),
				Check:  storedSecret("s3cr3t-value-2"),
			},
			{
				// Metadata change, same secret: description cleared through an explicit null.
				Config: providerHCL(m) + secretHCL("origin-auth-renamed", "", "s3cr3t-value-2"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("cdnpro_secret.auth", "name", "origin-auth-renamed"),
					resource.TestCheckNoResourceAttr("cdnpro_secret.auth", "description"),
					storedSecret("s3cr3t-value-2"),
				),
			},
			{
				ResourceName:      "cdnpro_secret.auth",
				ImportState:       true,
				ImportStateVerify: true,
			},
		},
	})
}

func webhookHCL(name, url string, withCredentials bool, secretKey string) string {
	creds := ""
	if withCredentials {
		creds = fmt.Sprintf(`
  credentials = {
    user       = "cdn-notifier"
    secret_key = %q
  }`, secretKey)
	}
	return fmt.Sprintf(`
resource "cdnpro_webhook" "notify" {
  name        = %q
  description = "deploy notifications"
  url         = %q%s
}
`, name, url, creds)
}

func TestAccWebhook(t *testing.T) {
	m := testAccSetup(t)
	storedKey := func(want string) resource.TestCheckFunc {
		return func(s *terraform.State) error {
			rs := s.RootModule().Resources["cdnpro_webhook.notify"]
			m.mu.Lock()
			defer m.mu.Unlock()
			wh, ok := m.webhooks[rs.Primary.ID]
			if !ok {
				return fmt.Errorf("webhook %s missing from mock", rs.Primary.ID)
			}
			got := ""
			if wh.credentials != nil {
				got, _ = wh.credentials["secretKey"].(string)
			}
			if got != want {
				return fmt.Errorf("mock stores secretKey %q, want %q", got, want)
			}
			return nil
		}
	}
	var firstID string
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6Factories(),
		Steps: []resource.TestStep{
			{
				Config: providerHCL(m) + webhookHCL("deploy-notify", "https://hooks.example.com/cdn", true, "key-one"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestMatchResourceAttr("cdnpro_webhook.notify", "id", regexp.MustCompile(`^wh\d+$`)),
					resource.TestCheckResourceAttr("cdnpro_webhook.notify", "credentials.user", "cdn-notifier"),
					resource.TestCheckNoResourceAttr("cdnpro_webhook.notify", "credentials.secret_key"),
					resource.TestCheckResourceAttr("cdnpro_webhook.notify", "total_calls", "0"),
					storedKey("key-one"),
					func(s *terraform.State) error {
						firstID = s.RootModule().Resources["cdnpro_webhook.notify"].Primary.ID
						return nil
					},
				),
			},
			{
				// Rotate the write-only key: patched in place, same ID.
				Config: providerHCL(m) + webhookHCL("deploy-notify", "https://hooks.example.com/cdn", true, "key-two"),
				Check: resource.ComposeAggregateTestCheckFunc(
					storedKey("key-two"),
					func(s *terraform.State) error {
						if id := s.RootModule().Resources["cdnpro_webhook.notify"].Primary.ID; id != firstID {
							return fmt.Errorf("webhook was replaced (%s -> %s) on a key rotation", firstID, id)
						}
						return nil
					},
				),
			},
			{
				// URL change only: no credentials sent, key untouched.
				Config: providerHCL(m) + webhookHCL("deploy-notify", "https://hooks.example.com/cdn-v2", true, "key-two"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("cdnpro_webhook.notify", "url", "https://hooks.example.com/cdn-v2"),
					storedKey("key-two"),
				),
			},
			{
				// Dropping credentials cannot be expressed in the API: the webhook is replaced.
				Config: providerHCL(m) + webhookHCL("deploy-notify", "https://hooks.example.com/cdn-v2", false, ""),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckNoResourceAttr("cdnpro_webhook.notify", "credentials"),
					storedKey(""),
					func(s *terraform.State) error {
						if id := s.RootModule().Resources["cdnpro_webhook.notify"].Primary.ID; id == firstID {
							return fmt.Errorf("webhook should have been replaced when credentials were removed")
						}
						return nil
					},
				),
			},
			{
				ResourceName:      "cdnpro_webhook.notify",
				ImportState:       true,
				ImportStateVerify: true,
			},
		},
	})
}

func logConfigHCL(hostnames string, extra string) string {
	return fmt.Sprintf(`
resource "cdnpro_log_config" "main" {
  description = "Standard access logs"
  hostnames   = [%s]%s
}
`, hostnames, extra)
}

func TestAccLogConfig(t *testing.T) {
	m := testAccSetup(t)
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6Factories(),
		CheckDestroy: func(*terraform.State) error {
			m.mu.Lock()
			defer m.mu.Unlock()
			if len(m.logConfigs) != 0 {
				return fmt.Errorf("log configs left behind: %d", len(m.logConfigs))
			}
			return nil
		},
		Steps: []resource.TestStep{
			{
				Config: providerHCL(m) + logConfigHCL(`"www.example.com"`, ""),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestMatchResourceAttr("cdnpro_log_config.main", "id", regexp.MustCompile(`^\d+$`)),
					resource.TestCheckResourceAttr("cdnpro_log_config.main", "log_download_storage_days", "14"),
					resource.TestCheckResourceAttr("cdnpro_log_config.main", "log_download_file_span_minutes", "30"),
					resource.TestMatchResourceAttr("cdnpro_log_config.main", "log_download_format", regexp.MustCompile(`^%cltip `)),
				),
			},
			{
				Config: providerHCL(m) + logConfigHCL(`"www.example.com", "example.com"`, `
  log_download_storage_days      = 30
  log_download_file_span_minutes = 60`),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("cdnpro_log_config.main", "hostnames.#", "2"),
					resource.TestCheckResourceAttr("cdnpro_log_config.main", "log_download_storage_days", "30"),
					resource.TestCheckResourceAttr("cdnpro_log_config.main", "log_download_file_span_minutes", "60"),
				),
			},
			{
				ResourceName:      "cdnpro_log_config.main",
				ImportState:       true,
				ImportStateVerify: true,
			},
		},
	})
}
