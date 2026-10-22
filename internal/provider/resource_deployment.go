package provider

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-framework-validators/listvalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"

	"github.com/mileweb/terraform-provider-cdnpro/internal/client"
)

var (
	_ resource.Resource                   = (*deploymentResource)(nil)
	_ resource.ResourceWithConfigure      = (*deploymentResource)(nil)
	_ resource.ResourceWithValidateConfig = (*deploymentResource)(nil)
)

// NewDeploymentResource returns the cdnpro_deployment resource.
func NewDeploymentResource() resource.Resource { return &deploymentResource{} }

type deploymentResource struct {
	data *ProviderData
}

type deploymentModel struct {
	ID             types.String `tfsdk:"id"`
	TaskID         types.String `tfsdk:"task_id"`
	Name           types.String `tfsdk:"name"`
	Target         types.String `tfsdk:"target"`
	Actions        types.List   `tfsdk:"actions"`
	Webhook        types.String `tfsdk:"webhook"`
	Status         types.String `tfsdk:"status"`
	StatusDetails  types.String `tfsdk:"status_details"`
	SubmissionTime types.String `tfsdk:"submission_time"`
	FinishTime     types.String `tfsdk:"finish_time"`
}

type deploymentActionModel struct {
	Action        types.String `tfsdk:"action"`
	PropertyID    types.String `tfsdk:"property_id"`
	CertificateID types.String `tfsdk:"certificate_id"`
	Version       types.Int64  `tfsdk:"version"`
}

var (
	deploymentSchemaOnce    = buildDeploymentSchema()
	deploymentActionObjType = nestedObjectType(deploymentSchemaOnce.Attributes["actions"])
)

func (r *deploymentResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_deployment"
}

func (r *deploymentResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = deploymentSchemaOnce
}

func (r *deploymentResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	r.data = configureProviderData(req.ProviderData, &resp.Diagnostics)
}

func buildDeploymentSchema() schema.Schema {
	return schema.Schema{
		MarkdownDescription: "Deploys (or removes) property and certificate versions to staging or production. " +
			"Before a `deploy_property` action the provider validates the version if needed. On create, actions whose " +
			"desired state already matches the live deployment are skipped, which is how an existing deployment is adopted. " +
			"Destroying a resource with `deploy_*` actions removes those versions from the target; `remove_*` actions " +
			"are not reversed. Import is not supported.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed: true,
				MarkdownDescription: "Stable identifier: the first deployment task submitted, or an `adopted:` marker when " +
					"the live state already matched on create.",
				PlanModifiers: []planmodifier.String{stringplanmodifier.UseStateForUnknown()},
			},
			"task_id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "ID of the most recent deployment task submitted by this resource, empty when none was needed.",
			},
			"name": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Label for the deployment task. Defaults to a generated name.",
			},
			"target": schema.StringAttribute{
				Required:      true,
				Validators:    []validator.String{stringvalidator.OneOf(client.TargetStaging, client.TargetProduction)},
				PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()},
			},
			"actions": schema.ListNestedAttribute{
				Required:   true,
				Validators: []validator.List{listvalidator.SizeAtLeast(1)},
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"action": schema.StringAttribute{
							Required: true,
							Validators: []validator.String{stringvalidator.OneOf(
								client.ActionDeployProperty, client.ActionRemoveProperty, client.ActionDeployCert, client.ActionRemoveCert)},
						},
						"property_id":    schema.StringAttribute{Optional: true},
						"certificate_id": schema.StringAttribute{Optional: true},
						"version": schema.Int64Attribute{
							Optional: true,
							MarkdownDescription: "Version to deploy. Required for `deploy_*` actions; typically " +
								"`cdnpro_property.x.latest_version`, or a pinned number to roll back. For `remove_*` actions the " +
								"provider records the version it found deployed out of band so the next plan shows the drift.",
						},
					},
				},
			},
			"webhook": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Webhook ID notified when the deployment task finishes.",
			},
			"status":          schema.StringAttribute{Computed: true},
			"status_details":  schema.StringAttribute{Computed: true},
			"submission_time": schema.StringAttribute{Computed: true},
			"finish_time":     schema.StringAttribute{Computed: true},
		},
	}
}

func (r *deploymentResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var cfg deploymentModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &cfg)...)
	if resp.Diagnostics.HasError() || cfg.Actions.IsNull() || cfg.Actions.IsUnknown() {
		return
	}
	var actions []deploymentActionModel
	resp.Diagnostics.Append(cfg.Actions.ElementsAs(ctx, &actions, false)...)
	for i, a := range actions {
		if a.Action.IsUnknown() {
			continue
		}
		p := path.Root("actions").AtListIndex(i)
		action := a.Action.ValueString()
		switch {
		case client.IsPropertyAction(action):
			if a.PropertyID.IsNull() {
				resp.Diagnostics.AddAttributeError(p.AtName("property_id"), "Missing property_id", action+" requires property_id.")
			}
			if !a.CertificateID.IsNull() {
				resp.Diagnostics.AddAttributeError(p.AtName("certificate_id"), "Unexpected certificate_id", action+" does not take certificate_id.")
			}
		default:
			if a.CertificateID.IsNull() {
				resp.Diagnostics.AddAttributeError(p.AtName("certificate_id"), "Missing certificate_id", action+" requires certificate_id.")
			}
			if !a.PropertyID.IsNull() {
				resp.Diagnostics.AddAttributeError(p.AtName("property_id"), "Unexpected property_id", action+" does not take property_id.")
			}
		}
		if client.IsDeployAction(action) && a.Version.IsNull() {
			resp.Diagnostics.AddAttributeError(p.AtName("version"), "Missing version", action+" requires version.")
		}
	}
}

// liveVersion returns the version of the action's property/certificate that is
// deployed to target, nil when none, and exists=false when the object is gone.
func (r *deploymentResource) liveVersion(ctx context.Context, a deploymentActionModel, target string) (deployed *int64, exists bool, err error) {
	if client.IsPropertyAction(a.Action.ValueString()) {
		p, _, err := r.data.Client.GetProperty(ctx, a.PropertyID.ValueString())
		if client.IsNotFound(err) {
			return nil, false, nil
		}
		if err != nil {
			return nil, false, err
		}
		if target == client.TargetProduction {
			return p.VersionInProduction, true, nil
		}
		return p.VersionInStaging, true, nil
	}
	c, _, err := r.data.Client.GetCertificate(ctx, a.CertificateID.ValueString())
	if client.IsNotFound(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if target == client.TargetProduction {
		return c.VersionInProduction, true, nil
	}
	return c.VersionInStaging, true, nil
}

// satisfied reports whether the live state already matches the action.
func satisfied(a deploymentActionModel, deployed *int64) bool {
	if client.IsDeployAction(a.Action.ValueString()) {
		return deployed != nil && !a.Version.IsNull() && *deployed == a.Version.ValueInt64()
	}
	return deployed == nil
}

func (r *deploymentResource) actionsOf(ctx context.Context, m *deploymentModel, diags *diag.Diagnostics) []deploymentActionModel {
	var actions []deploymentActionModel
	if m.Actions.IsNull() || m.Actions.IsUnknown() {
		return nil
	}
	diags.Append(m.Actions.ElementsAs(ctx, &actions, false)...)
	return actions
}

func toAPIActions(actions []deploymentActionModel) []client.DeploymentAction {
	out := make([]client.DeploymentAction, 0, len(actions))
	for _, a := range actions {
		da := client.DeploymentAction{
			Action:        a.Action.ValueString(),
			PropertyID:    a.PropertyID.ValueString(),
			CertificateID: a.CertificateID.ValueString(),
		}
		if client.IsDeployAction(da.Action) && !a.Version.IsNull() {
			v := client.FlexibleInt(a.Version.ValueInt64())
			da.Version = &v
		}
		out = append(out, da)
	}
	return out
}

func (r *deploymentResource) taskName(m *deploymentModel) string {
	if !m.Name.IsNull() && !m.Name.IsUnknown() && m.Name.ValueString() != "" {
		return m.Name.ValueString()
	}
	return fmt.Sprintf("terraform-%s-%s", m.Target.ValueString(), time.Now().UTC().Format("20060102T150405Z"))
}

// ensure makes the live deployment match plan: it validates property versions
// that need it, submits one deployment task for the actions that are not yet
// satisfied, waits for it, and fills the computed attributes. prior carries the
// previous computed values for the no-op case.
func (r *deploymentResource) ensure(ctx context.Context, plan *deploymentModel, prior *deploymentModel, diags *diag.Diagnostics) {
	target := plan.Target.ValueString()
	actions := r.actionsOf(ctx, plan, diags)
	if diags.HasError() {
		return
	}

	var pending []deploymentActionModel
	for i, a := range actions {
		deployed, exists, err := r.liveVersion(ctx, a, target)
		if err != nil {
			addAPIError(diags, fmt.Sprintf("Reading deployment state for actions[%d]", i), err)
			return
		}
		if !exists {
			id := a.PropertyID.ValueString() + a.CertificateID.ValueString()
			diags.AddAttributeError(path.Root("actions").AtListIndex(i), "Referenced object not found",
				fmt.Sprintf("%s refers to %s, which does not exist.", a.Action.ValueString(), id))
			return
		}
		if !satisfied(a, deployed) {
			pending = append(pending, a)
		}
	}

	if len(pending) == 0 {
		// Nothing to do: adopt the live state without an API call.
		if prior != nil && !prior.ID.IsNull() && !prior.ID.IsUnknown() && prior.ID.ValueString() != "" {
			plan.ID = prior.ID
			plan.TaskID = prior.TaskID
			plan.Status, plan.StatusDetails = prior.Status, prior.StatusDetails
			plan.SubmissionTime, plan.FinishTime = prior.SubmissionTime, prior.FinishTime
			if plan.Status.IsUnknown() || plan.Status.IsNull() {
				plan.Status = types.StringValue("succeeded")
			}
			fillUnknownStrings(&plan.TaskID, &plan.StatusDetails, &plan.SubmissionTime, &plan.FinishTime)
		} else {
			plan.ID = types.StringValue(fmt.Sprintf("adopted:%s:%d", target, time.Now().UnixNano()))
			plan.TaskID = types.StringValue("")
			plan.Status = types.StringValue("succeeded")
			plan.StatusDetails = types.StringValue("live deployment already matched the configuration; no task submitted")
			plan.SubmissionTime = types.StringValue("")
			plan.FinishTime = types.StringValue("")
		}
		return
	}

	// Validate property versions that need it.
	for _, a := range pending {
		if a.Action.ValueString() != client.ActionDeployProperty {
			continue
		}
		if err := r.ensureValidated(ctx, a.PropertyID.ValueString(), a.Version.ValueInt64()); err != nil {
			addAPIError(diags, fmt.Sprintf("Validating property %s version %d", a.PropertyID.ValueString(), a.Version.ValueInt64()), err)
			return
		}
	}

	req := client.DeploymentTaskRequest{
		Name:    r.taskName(plan),
		Target:  target,
		Actions: toAPIActions(pending),
		Webhook: strPtrNonEmpty(plan.Webhook),
	}
	task, err := r.submitAndWait(ctx, req, pending)
	if err != nil {
		addAPIError(diags, "Deploying to "+target, err)
		return
	}
	if prior != nil && !prior.ID.IsNull() && !prior.ID.IsUnknown() && prior.ID.ValueString() != "" {
		plan.ID = prior.ID
	} else {
		plan.ID = types.StringValue(task.ID)
	}
	plan.TaskID = types.StringValue(task.ID)
	plan.Status = types.StringValue(task.Status)
	plan.StatusDetails = types.StringValue(task.StatusDetails)
	plan.SubmissionTime = types.StringValue(task.SubmissionTime)
	plan.FinishTime = types.StringValue(task.FinishTime)
}

func fillUnknownStrings(vals ...*types.String) {
	for _, v := range vals {
		if v.IsUnknown() || v.IsNull() {
			*v = types.StringValue("")
		}
	}
}

// ensureValidated runs a validation for the property version unless it is
// already marked Validated.
func (r *deploymentResource) ensureValidated(ctx context.Context, propertyID string, version int64) error {
	pv, err := r.data.Client.GetPropertyVersion(ctx, propertyID, fmt.Sprintf("%d", version))
	if err != nil {
		return err
	}
	if pv.Status.IsValidated() {
		return nil
	}
	return r.validate(ctx, propertyID, version)
}

func (r *deploymentResource) validate(ctx context.Context, propertyID string, version int64) error {
	name := fmt.Sprintf("terraform-validate-%s-v%d", propertyID, version)
	_, err := r.data.Client.ValidatePropertyVersion(ctx, name, propertyID, version, r.data.ValidationPoll)
	return err
}

// submitAndWait submits the task and waits for it. On OutdatedSecret it
// re-validates the affected property versions once and retries.
func (r *deploymentResource) submitAndWait(ctx context.Context, req client.DeploymentTaskRequest, actions []deploymentActionModel) (*client.DeploymentTask, error) {
	id, err := r.data.Client.CreateDeploymentTask(ctx, req)
	if client.HasCode(err, client.CodeOutdatedSecret) {
		for _, a := range actions {
			if a.Action.ValueString() != client.ActionDeployProperty {
				continue
			}
			if vErr := r.validate(ctx, a.PropertyID.ValueString(), a.Version.ValueInt64()); vErr != nil {
				return nil, fmt.Errorf("re-validating after OutdatedSecret: %w", vErr)
			}
		}
		id, err = r.data.Client.CreateDeploymentTask(ctx, req)
	}
	if err != nil {
		return nil, err
	}
	return r.data.Client.WaitForDeploymentTask(ctx, id, r.data.DeploymentPoll)
}

func (r *deploymentResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan deploymentModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	r.ensure(ctx, &plan, nil, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *deploymentResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state deploymentModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	target := state.Target.ValueString()
	actions := r.actionsOf(ctx, &state, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}

	for i := range actions {
		a := &actions[i]
		deployed, exists, err := r.liveVersion(ctx, *a, target)
		if err != nil {
			addAPIError(&resp.Diagnostics, fmt.Sprintf("Reading deployment state for actions[%d]", i), err)
			return
		}
		if !exists {
			// The property/certificate is gone, so this deployment no longer exists either.
			resp.State.RemoveResource(ctx)
			return
		}
		// Record what is actually deployed so the plan shows drift: for deploy_*
		// a different (or missing) version, for remove_* any version at all.
		a.Version = int64FromPtr(deployed)
	}
	l, d := types.ListValueFrom(ctx, deploymentActionObjType, actions)
	resp.Diagnostics.Append(d...)
	state.Actions = l

	// Refresh the task status when a real task was submitted.
	if id := state.TaskID.ValueString(); id != "" && !strings.HasPrefix(id, "adopted:") {
		if task, err := r.data.Client.GetDeploymentTask(ctx, id); err == nil {
			state.Status = types.StringValue(task.Status)
			state.StatusDetails = types.StringValue(task.StatusDetails)
			state.SubmissionTime = types.StringValue(task.SubmissionTime)
			state.FinishTime = types.StringValue(task.FinishTime)
		}
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *deploymentResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state deploymentModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	r.ensure(ctx, &plan, &state, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *deploymentResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state deploymentModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	target := state.Target.ValueString()
	actions := r.actionsOf(ctx, &state, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}

	var inverse []client.DeploymentAction
	for _, a := range actions {
		action := a.Action.ValueString()
		if !client.IsDeployAction(action) {
			continue // remove_* has no safe inverse
		}
		deployed, exists, err := r.liveVersion(ctx, a, target)
		if err != nil {
			addAPIError(&resp.Diagnostics, "Reading deployment state", err)
			return
		}
		if !exists || deployed == nil {
			continue // already gone or already undeployed
		}
		inverse = append(inverse, client.DeploymentAction{
			Action:        client.InverseAction(action),
			PropertyID:    a.PropertyID.ValueString(),
			CertificateID: a.CertificateID.ValueString(),
		})
	}
	if len(inverse) == 0 {
		return
	}
	req2 := client.DeploymentTaskRequest{
		Name:    fmt.Sprintf("terraform-undeploy-%s-%s", target, time.Now().UTC().Format("20060102T150405Z")),
		Target:  target,
		Actions: inverse,
		Webhook: strPtrNonEmpty(state.Webhook),
	}
	id, err := r.data.Client.CreateDeploymentTask(ctx, req2)
	if err != nil {
		addAPIError(&resp.Diagnostics, "Removing deployment from "+target, err)
		return
	}
	if _, err := r.data.Client.WaitForDeploymentTask(ctx, id, r.data.DeploymentPoll); err != nil {
		addAPIError(&resp.Diagnostics, "Waiting for removal from "+target, err)
	}
}
