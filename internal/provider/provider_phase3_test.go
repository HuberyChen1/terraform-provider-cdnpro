package provider

import (
	"fmt"
	"regexp"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

func TestAccDataSources(t *testing.T) {
	m := testAccSetup(t)
	id := m.seedDeployedProperty("lookup", "production")
	config := providerHCL(m) + `
data "cdnpro_shields" "all" {}
data "cdnpro_shields" "used" {
  used_in_properties = true
}
data "cdnpro_isps" "all" {}
data "cdnpro_isps" "us" {
  region_code = "na.us"
}
data "cdnpro_client_regions" "all" {}
data "cdnpro_client_regions" "att" {
  isp_code = "att"
}
data "cdnpro_hostname" "known" {
  hostname = "adopt.example.com"
}
data "cdnpro_hostname" "unknown" {
  hostname = "nobody.example.com"
}
`
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6Factories(),
		Steps: []resource.TestStep{{
			Config: config,
			Check: resource.ComposeAggregateTestCheckFunc(
				resource.TestCheckResourceAttr("data.cdnpro_shields.all", "shields.#", "2"),
				// Sorted by id: shield-eu-1 before shield-us-1.
				resource.TestCheckResourceAttr("data.cdnpro_shields.all", "shields.0.id", "shield-eu-1"),
				resource.TestCheckResourceAttr("data.cdnpro_shields.all", "shields.0.ipv6.#", "0"),
				resource.TestCheckResourceAttr("data.cdnpro_shields.all", "shields.1.ipv4.0", "203.0.113.0/24"),
				resource.TestCheckResourceAttr("data.cdnpro_shields.used", "shields.#", "1"),
				resource.TestCheckResourceAttr("data.cdnpro_shields.used", "shields.0.id", "shield-us-1"),

				resource.TestCheckResourceAttr("data.cdnpro_isps.all", "isps.#", "3"),
				resource.TestCheckResourceAttr("data.cdnpro_isps.all", "isps.0.code", "att"),
				resource.TestCheckResourceAttr("data.cdnpro_isps.us", "isps.#", "2"),

				resource.TestCheckResourceAttr("data.cdnpro_client_regions.all", "regions.#", "3"),
				resource.TestCheckResourceAttr("data.cdnpro_client_regions.att", "regions.#", "1"),
				resource.TestCheckResourceAttr("data.cdnpro_client_regions.att", "regions.0.name", "United States"),

				resource.TestCheckResourceAttr("data.cdnpro_hostname.known", "found", "true"),
				resource.TestCheckResourceAttr("data.cdnpro_hostname.known", "property_in_production.property_id", id),
				resource.TestCheckResourceAttr("data.cdnpro_hostname.known", "property_in_production.version", "1"),
				resource.TestCheckNoResourceAttr("data.cdnpro_hostname.known", "property_in_staging"),
				resource.TestCheckResourceAttr("data.cdnpro_hostname.unknown", "found", "false"),
			),
		}},
	})
}

// Actions are exercised through terraform_data's lifecycle.action_trigger so
// they run as part of an ordinary apply.
func purgeActionHCL(dirURL string) string {
	return fmt.Sprintf(`
action "cdnpro_purge" "assets" {
  config {
    name     = "acc purge"
    target   = "production"
    dir_urls = [%q]
    file_headers = [
      { name = "Accept-Encoding", value = "gzip" },
    ]
  }
}

resource "terraform_data" "trigger" {
  input = %q
  lifecycle {
    action_trigger {
      events  = [after_create, after_update]
      actions = [action.cdnpro_purge.assets]
    }
  }
}
`, dirURL, dirURL)
}

func TestAccPurgeAction(t *testing.T) {
	m := testAccSetup(t)
	purgeCount := func(want int) resource.TestCheckFunc {
		return func(*terraform.State) error {
			m.mu.Lock()
			defer m.mu.Unlock()
			if len(m.purgePosts) != want {
				return fmt.Errorf("mock saw %d purge POSTs, want %d", len(m.purgePosts), want)
			}
			return nil
		}
	}
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6Factories(),
		Steps: []resource.TestStep{
			{
				Config: providerHCL(m) + purgeActionHCL("https://www.example.com/assets/"),
				Check: resource.ComposeAggregateTestCheckFunc(
					purgeCount(1),
					func(*terraform.State) error {
						m.mu.Lock()
						defer m.mu.Unlock()
						body := m.purgePosts[0]
						if body["target"] != "production" || body["name"] != "acc purge" {
							return fmt.Errorf("unexpected purge body: %v", body)
						}
						if hs, _ := body["fileHeaders"].([]any); len(hs) != 1 {
							return fmt.Errorf("fileHeaders not forwarded: %v", body)
						}
						if _, present := body["action"]; present {
							return fmt.Errorf("action should be omitted when unset: %v", body)
						}
						return nil
					},
				),
			},
			{
				// Same config: no update, no purge.
				Config: providerHCL(m) + purgeActionHCL("https://www.example.com/assets/"),
				Check:  purgeCount(1),
			},
			{
				// A partial success rate fails the apply with a clear message.
				Config:      providerHCL(m) + purgeActionHCL("https://www.example.com/partial/"),
				ExpectError: regexp.MustCompile(`Purge partially failed[\s\S]*50\.0%`),
			},
			{
				// An undeployed host surfaces the API code plus the target hint.
				Config:      providerHCL(m) + purgeActionHCL("https://undeployed.example.com/"),
				ExpectError: regexp.MustCompile(`InvalidPurgeUrl[\s\S]*deployed\s+to\s+the\s+chosen\s+target`),
			},
		},
	})
}

func TestAccPrefetchAction(t *testing.T) {
	m := testAccSetup(t)
	config := providerHCL(m) + `
action "cdnpro_prefetch" "warm" {
  config {
    file_list = [
      { url = "https://www.example.com/app.js" },
      {
        url     = "https://www.example.com/app.css"
        headers = [{ name = "Accept-Encoding", value = "br" }]
      },
    ]
    regions        = ["Asia", "Europe"]
    prefetch_level = "Edge"
  }
}

resource "terraform_data" "trigger" {
  input = "v1"
  lifecycle {
    action_trigger {
      events  = [after_create]
      actions = [action.cdnpro_prefetch.warm]
    }
  }
}
`
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6Factories(),
		Steps: []resource.TestStep{{
			Config: config,
			Check: func(*terraform.State) error {
				m.mu.Lock()
				defer m.mu.Unlock()
				if len(m.prefetchPosts) != 1 {
					return fmt.Errorf("mock saw %d prefetch POSTs, want 1", len(m.prefetchPosts))
				}
				body := m.prefetchPosts[0]
				files, _ := body["fileList"].([]any)
				if len(files) != 2 {
					return fmt.Errorf("fileList not forwarded: %v", body)
				}
				second, _ := files[1].(map[string]any)
				if hs, _ := second["headers"].([]any); len(hs) != 1 {
					return fmt.Errorf("per-file headers not forwarded: %v", second)
				}
				if regions, _ := body["regions"].([]any); len(regions) != 2 {
					return fmt.Errorf("regions not forwarded: %v", body)
				}
				if body["prefetchLevel"] != "Edge" {
					return fmt.Errorf("prefetchLevel not forwarded: %v", body)
				}
				if _, present := body["target"]; present {
					return fmt.Errorf("prefetch must not send target: %v", body)
				}
				return nil
			},
		}},
	})
}

func TestAccPrefetchActionRejectsPastStartTime(t *testing.T) {
	m := testAccSetup(t)
	config := providerHCL(m) + `
action "cdnpro_prefetch" "late" {
  config {
    file_list  = [{ url = "https://www.example.com/app.js" }]
    start_time = "2020-01-01T00:00:00Z"
  }
}

resource "terraform_data" "trigger" {
  input = "v1"
  lifecycle {
    action_trigger {
      events  = [after_create]
      actions = [action.cdnpro_prefetch.late]
    }
  }
}
`
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoV6Factories(),
		Steps: []resource.TestStep{{
			Config:      config,
			ExpectError: regexp.MustCompile(`start_time out of range`),
		}},
	})
}
