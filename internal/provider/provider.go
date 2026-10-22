// Package provider wires the cdnpro Terraform provider: configuration, the shared
// API client and the list of resources and data sources.
package provider

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/action"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/mileweb/terraform-provider-cdnpro/internal/client"
)

const (
	envAPIUser = "CDNPRO_API_USER"
	envAPIKey  = "CDNPRO_API_KEY"
	envServer  = "CDNPRO_SERVER"
)

// Ensure the provider satisfies the framework interfaces.
var (
	_ provider.Provider            = (*cdnproProvider)(nil)
	_ provider.ProviderWithActions = (*cdnproProvider)(nil)
)

// New returns a provider factory for the given build version.
func New(version string) func() provider.Provider {
	return func() provider.Provider {
		return &cdnproProvider{version: version}
	}
}

type cdnproProvider struct {
	version string
}

// providerModel maps the provider block.
type providerModel struct {
	APIUser types.String  `tfsdk:"api_user"`
	APIKey  types.String  `tfsdk:"api_key"`
	Server  types.String  `tfsdk:"server"`
	Polling *pollingModel `tfsdk:"polling"`
}

type pollingModel struct {
	ValidationInterval   types.String `tfsdk:"validation_interval"`
	ValidationTimeout    types.String `tfsdk:"validation_timeout"`
	DeploymentInterval   types.String `tfsdk:"deployment_interval"`
	DeploymentTimeout    types.String `tfsdk:"deployment_timeout"`
	EdgeHostnameInterval types.String `tfsdk:"edge_hostname_interval"`
	EdgeHostnameTimeout  types.String `tfsdk:"edge_hostname_timeout"`
	PurgeInterval        types.String `tfsdk:"purge_interval"`
	PurgeTimeout         types.String `tfsdk:"purge_timeout"`
}

// ProviderData is handed to every resource and data source via Configure.
type ProviderData struct {
	Client           *client.Client
	ValidationPoll   client.PollConfig
	DeploymentPoll   client.PollConfig
	EdgeHostnamePoll client.PollConfig
	PurgePoll        client.PollConfig
}

func (p *cdnproProvider) Metadata(_ context.Context, _ provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName = "cdnpro"
	resp.Version = p.version
}

func (p *cdnproProvider) Schema(_ context.Context, _ provider.SchemaRequest, resp *provider.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manage CDN Pro configuration (properties, certificates, edge hostnames and " +
			"deployments) through the CDN Pro API. Every request is signed with the API user and " +
			"key using the API's HMAC-SHA1 scheme; the key is never logged.",
		Attributes: map[string]schema.Attribute{
			"api_user": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "API user (login name). Falls back to the `" + envAPIUser + "` environment variable.",
			},
			"api_key": schema.StringAttribute{
				Optional:            true,
				Sensitive:           true,
				MarkdownDescription: "API key used to sign requests. Falls back to the `" + envAPIKey + "` environment variable.",
			},
			"server": schema.StringAttribute{
				Optional: true,
				MarkdownDescription: "API server, for example `https://ngapi.cdnetworks.com`. Falls back to the `" +
					envServer + "` environment variable.",
			},
		},
		Blocks: map[string]schema.Block{
			"polling": schema.SingleNestedBlock{
				MarkdownDescription: "Overrides for the fixed-interval polling the provider does on asynchronous " +
					"operations. Values are Go durations such as `15s` or `20m`.",
				Attributes: map[string]schema.Attribute{
					"validation_interval":    durationAttr("Interval between property validation status checks.", client.DefaultValidationPoll.Interval),
					"validation_timeout":     durationAttr("Maximum time to wait for a property validation.", client.DefaultValidationPoll.Timeout),
					"deployment_interval":    durationAttr("Interval between deployment task status checks.", client.DefaultDeploymentPoll.Interval),
					"deployment_timeout":     durationAttr("Maximum time to wait for a deployment task.", client.DefaultDeploymentPoll.Timeout),
					"edge_hostname_interval": durationAttr("Interval between edge hostname provisioning checks.", client.DefaultEdgeHostnamePoll.Interval),
					"edge_hostname_timeout":  durationAttr("Maximum time to wait for an edge hostname operation.", client.DefaultEdgeHostnamePoll.Timeout),
					"purge_interval":         durationAttr("Interval between purge/prefetch status checks.", client.DefaultPurgePoll.Interval),
					"purge_timeout":          durationAttr("Maximum time to wait for a purge or prefetch.", client.DefaultPurgePoll.Timeout),
				},
			},
		},
	}
}

func durationAttr(description string, def time.Duration) schema.StringAttribute {
	return schema.StringAttribute{
		Optional:            true,
		MarkdownDescription: fmt.Sprintf("%s Defaults to `%s`.", description, def),
	}
}

func (p *cdnproProvider) Configure(ctx context.Context, req provider.ConfigureRequest, resp *provider.ConfigureResponse) {
	var cfg providerModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() {
		return
	}

	apiUser := stringOrEnv(cfg.APIUser, envAPIUser)
	apiKey := stringOrEnv(cfg.APIKey, envAPIKey)
	server := stringOrEnv(cfg.Server, envServer)

	if cfg.APIUser.IsUnknown() || cfg.APIKey.IsUnknown() || cfg.Server.IsUnknown() {
		resp.Diagnostics.AddError("Unknown provider configuration",
			"api_user, api_key and server must be known at plan time. Use static values, variables or environment variables.")
		return
	}
	if apiUser == "" {
		resp.Diagnostics.AddAttributeError(path.Root("api_user"), "Missing API user",
			"Set api_user in the provider block or the "+envAPIUser+" environment variable.")
	}
	if apiKey == "" {
		resp.Diagnostics.AddAttributeError(path.Root("api_key"), "Missing API key",
			"Set api_key in the provider block or the "+envAPIKey+" environment variable.")
	}
	if server == "" {
		resp.Diagnostics.AddAttributeError(path.Root("server"), "Missing API server",
			"Set server in the provider block or the "+envServer+" environment variable.")
	}
	if resp.Diagnostics.HasError() {
		return
	}

	c, err := client.New(client.Config{
		Server:    server,
		APIUser:   apiUser,
		APIKey:    apiKey,
		UserAgent: "terraform-provider-cdnpro/" + p.version,
	})
	if err != nil {
		resp.Diagnostics.AddError("Invalid provider configuration", err.Error())
		return
	}

	data := &ProviderData{
		Client:           c,
		ValidationPoll:   client.DefaultValidationPoll,
		DeploymentPoll:   client.DefaultDeploymentPoll,
		EdgeHostnamePoll: client.DefaultEdgeHostnamePoll,
		PurgePoll:        client.DefaultPurgePoll,
	}
	if cfg.Polling != nil {
		pollPath := path.Root("polling")
		data.ValidationPoll.Interval = parseDuration(&resp.Diagnostics, pollPath.AtName("validation_interval"), cfg.Polling.ValidationInterval, data.ValidationPoll.Interval)
		data.ValidationPoll.Timeout = parseDuration(&resp.Diagnostics, pollPath.AtName("validation_timeout"), cfg.Polling.ValidationTimeout, data.ValidationPoll.Timeout)
		data.DeploymentPoll.Interval = parseDuration(&resp.Diagnostics, pollPath.AtName("deployment_interval"), cfg.Polling.DeploymentInterval, data.DeploymentPoll.Interval)
		data.DeploymentPoll.Timeout = parseDuration(&resp.Diagnostics, pollPath.AtName("deployment_timeout"), cfg.Polling.DeploymentTimeout, data.DeploymentPoll.Timeout)
		data.EdgeHostnamePoll.Interval = parseDuration(&resp.Diagnostics, pollPath.AtName("edge_hostname_interval"), cfg.Polling.EdgeHostnameInterval, data.EdgeHostnamePoll.Interval)
		data.EdgeHostnamePoll.Timeout = parseDuration(&resp.Diagnostics, pollPath.AtName("edge_hostname_timeout"), cfg.Polling.EdgeHostnameTimeout, data.EdgeHostnamePoll.Timeout)
		data.PurgePoll.Interval = parseDuration(&resp.Diagnostics, pollPath.AtName("purge_interval"), cfg.Polling.PurgeInterval, data.PurgePoll.Interval)
		data.PurgePoll.Timeout = parseDuration(&resp.Diagnostics, pollPath.AtName("purge_timeout"), cfg.Polling.PurgeTimeout, data.PurgePoll.Timeout)
	}
	if resp.Diagnostics.HasError() {
		return
	}

	resp.ResourceData = data
	resp.DataSourceData = data
	resp.ActionData = data
}

func (p *cdnproProvider) Resources(_ context.Context) []func() resource.Resource {
	return []func() resource.Resource{
		NewPropertyResource,
		NewCertificateResource,
		NewEdgeHostnameResource,
		NewDeploymentResource,
		NewSecretResource,
		NewWebhookResource,
		NewLogConfigResource,
	}
}

func (p *cdnproProvider) DataSources(_ context.Context) []func() datasource.DataSource {
	return []func() datasource.DataSource{
		NewHostnameDataSource,
		NewShieldsDataSource,
		NewISPsDataSource,
		NewClientRegionsDataSource,
	}
}

func (p *cdnproProvider) Actions(_ context.Context) []func() action.Action {
	return []func() action.Action{
		NewPurgeAction,
		NewPrefetchAction,
	}
}

func stringOrEnv(v types.String, env string) string {
	if !v.IsNull() && !v.IsUnknown() && v.ValueString() != "" {
		return v.ValueString()
	}
	return os.Getenv(env)
}

func parseDuration(diags *diag.Diagnostics, p path.Path, v types.String, def time.Duration) time.Duration {
	if v.IsNull() || v.IsUnknown() || v.ValueString() == "" {
		return def
	}
	d, err := time.ParseDuration(v.ValueString())
	if err != nil || d <= 0 {
		diags.AddAttributeError(p, "Invalid duration",
			fmt.Sprintf("%q is not a positive Go duration such as \"30s\" or \"10m\".", v.ValueString()))
		return def
	}
	return d
}

// configureProviderData extracts ProviderData from a resource Configure request,
// tolerating the nil ProviderData the framework passes before provider
// configuration has run.
func configureProviderData(raw any, diags *diag.Diagnostics) *ProviderData {
	if raw == nil {
		return nil
	}
	data, ok := raw.(*ProviderData)
	if !ok {
		diags.AddError("Unexpected provider data",
			fmt.Sprintf("Expected *ProviderData, got %T. This is a bug in the provider.", raw))
		return nil
	}
	return data
}
