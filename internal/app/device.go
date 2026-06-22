package app

import (
	"encoding/base64"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/ghostable-dev/beta/internal/cli"
	"github.com/ghostable-dev/beta/internal/domain"
	"github.com/ghostable-dev/beta/internal/prompt"
	"github.com/ghostable-dev/beta/internal/store"
)

var deviceCommandOptions = []commandOption{
	{Label: "status", Description: "Show this device's access status"},
	{Label: "approvers", Description: "Show devices that can grant access"},
	{Label: "requests", Description: "Create or review access requests"},
	{Label: "list", Description: "List project devices"},
	{Label: "create", Description: "Create an access credential"},
	{Label: "join", Description: "Add this machine as a device"},
	{Label: "share", Description: "Grant access to another device"},
	{Label: "revoke", Description: "Remove device access"},
	{Label: "delete", Description: "Delete a revoked device record"},
}

var deviceAdvancedCommandOptions = []commandOption{
	{Label: "grants", Description: "Advanced: list environment grant records"},
	{Label: "matrix", Description: "Advanced: show roles by device and environment"},
}

var accessRequestCommandOptions = []commandOption{
	{Label: "list", Description: "List pending access requests"},
	{Label: "create", Description: "Create an access request"},
	{Label: "approve", Description: "Approve an access request"},
	{Label: "deny", Description: "Deny an access request"},
}

func (r *Runner) runDevice(args []string) error {
	if len(args) == 0 {
		if !r.interactive {
			r.printDeviceHelp()
			return nil
		}
		selected, err := r.selectCommand(r.deviceSelectLabel(), deviceCommandOptions)
		if err != nil {
			return err
		}
		args = append(args, selected)
	}
	if isHelpArg(args[0]) {
		r.printDeviceHelp()
		return nil
	}

	switch args[0] {
	case "create":
		return r.runAutomationCredentialCreate(args[1:], "access", r.deviceCommandName()+" create")
	case "join":
		return r.runDeviceJoin(args[1:])
	case "list":
		return r.runDeviceList(args[1:])
	case "status":
		return r.runDeviceStatus(args[1:])
	case "approvers":
		return r.runDeviceApprovers(args[1:])
	case "share":
		return r.runDeviceShare(args[1:])
	case "grants", "access":
		return r.runDeviceGrants(args[1:])
	case "matrix":
		return r.runDeviceMatrix(args[1:])
	case "revoke":
		return r.runDeviceRevoke(args[1:])
	case "delete":
		return r.runDeviceDelete(args[1:])
	case "requests":
		return r.runDeviceRequests(args[1:])
	case "leave":
		return fmt.Errorf("%s %s is not implemented in the Go client yet", r.deviceCommandName(), args[0])
	default:
		return fmt.Errorf("unknown %s command %q", r.deviceCommandName(), args[0])
	}
}

func (r *Runner) printDeviceHelp() {
	fmt.Fprintf(r.out, "Usage: ghostable %s <status|approvers|requests|list|create|join|share|revoke|delete> [options]\n", r.deviceCommandName())
	fmt.Fprintln(r.out)
	fmt.Fprintln(r.out, warn("Commands:"))
	printCommandDescriptions(r.out, deviceCommandOptions)
	fmt.Fprintln(r.out)
	fmt.Fprintln(r.out, warn("Advanced views:"))
	printCommandDescriptions(r.out, deviceAdvancedCommandOptions)
}

func (r *Runner) deviceCommandName() string {
	if len(r.args) > 1 && r.args[1] == "access" {
		return "access"
	}
	return "device"
}

func (r *Runner) deviceSelectLabel() string {
	if r.deviceCommandName() == "access" {
		return "Select an access command"
	}
	return "Select a device command"
}

func (r *Runner) runDeviceJoin(args []string) error {
	fs := newFlagSet(r.deviceCommandName()+" join", r.errOut)
	name := fs.String("name", "", "Device label")
	platform := fs.String("platform", "", "Device platform label")
	force := fs.Bool("force", false, "Create a new local device identity even if this machine already has one")
	jsonOut := fs.Bool("json", false, "Print join result as JSON")
	if _, err := cli.Parse(fs, args, cli.BoolFlags("force", "json")); err != nil {
		return err
	}
	repo, err := r.openRepo()
	hasLocalIdentity := err == nil
	if err != nil {
		if !isMissingLocalIdentityError(err) {
			return err
		}
		repo, err = store.OpenProject(".")
		if err != nil {
			return err
		}
	}
	if hasLocalIdentity && !*force {
		devices, err := repo.Devices()
		if err != nil {
			return err
		}
		if currentDevice, ok := accessDeviceByID(devices, repo.DeviceID()); ok {
			return fmt.Errorf("this machine is already joined as %s; use --force only if you intentionally want to replace the local device identity", accessDeviceDisplayDevice(currentDevice, false))
		}
	}
	deviceName, err := r.ask("Device label", *name, defaultDeviceName(), "name")
	if err != nil {
		return err
	}
	device, created, err := repo.JoinDevice(deviceName, *platform)
	if err != nil {
		return err
	}
	payload := map[string]interface{}{"device": device, "createdLocalKey": created, "keyPath": repo.KeyPath()}
	if *jsonOut {
		return printJSON(r.out, payload)
	}
	fmt.Fprintln(r.out, success(fmt.Sprintf("Joined as %s.", accessDeviceDisplayDevice(device, false))))
	printAccessDetailRows(r.out, []accessDetailRow{
		{Label: "Device", Value: statusDeviceName(device)},
		{Label: "Device ID", Value: device.ID},
		{Label: "Platform", Value: statusDevicePlatform(device)},
		{Label: "Status", Value: deviceStatusDisplay(device.Status)},
		{Label: "Local key", Value: repo.KeyPath()},
	})
	r.printJoinNextSteps(repo, device)
	return nil
}

func isMissingLocalIdentityError(err error) bool {
	if err == nil {
		return false
	}
	text := err.Error()
	return strings.Contains(text, "no local Ghostable identity") || strings.Contains(text, "has no local Ghostable identity")
}

func (r *Runner) printJoinNextSteps(repo store.Repository, device domain.DeviceRecord) {
	fmt.Fprintln(r.out)
	fmt.Fprintln(r.out, warn("Next steps:"))
	fmt.Fprintf(r.out, "  Commit the public device record: %s\n", deviceRecordRelativePath(device.ID))
	fmt.Fprintln(r.out, "  Commit the new device.joined event under .ghostable/events/.")
	fmt.Fprintln(r.out)
	fmt.Fprintln(r.out, "  To request access:")
	fmt.Fprintln(r.out, "    ghostable access requests create --env <env> --role reader")
	fmt.Fprintln(r.out)
	fmt.Fprintln(r.out, "  Or ask an owner/grantor to run:")
	fmt.Fprintf(r.out, "    ghostable access share --device-id %s --env <env> --role reader\n", device.ID)

	approvers, err := accessApprovers(repo, "")
	if err != nil || len(approvers) == 0 {
		return
	}

	fmt.Fprintln(r.out)
	fmt.Fprintln(r.out, warn("Devices that can grant access:"))
	printAccessApproverRows(r, approvers, false)
}

func deviceRecordRelativePath(deviceID string) string {
	return ".ghostable/devices/" + base64.RawURLEncoding.EncodeToString([]byte(deviceID)) + ".json"
}

func (r *Runner) runDeviceList(args []string) error {
	fs := newFlagSet(r.deviceCommandName()+" list", r.errOut)
	full := fs.Bool("full", false, "Show full device IDs and values")
	jsonOut := fs.Bool("json", false, "Print raw device records as JSON")
	if _, err := cli.Parse(fs, args, cli.BoolFlags("full", "json")); err != nil {
		return err
	}
	repo, err := r.openRepo()
	if err != nil {
		return err
	}
	devices, err := repo.Devices()
	if err != nil {
		return err
	}
	if *jsonOut {
		return printJSON(r.out, devices)
	}
	printAccessDeviceRows(r.out, devices, repo.DeviceID(), *full)
	return nil
}

func (r *Runner) runDeviceStatus(args []string) error {
	fs := newFlagSet(r.deviceCommandName()+" status", r.errOut)
	jsonOut := fs.Bool("json", false, "Print local device status as JSON")
	if _, err := cli.Parse(fs, args, cli.BoolFlags("json")); err != nil {
		return err
	}
	repo, err := r.openRepo()
	if err != nil {
		return err
	}
	devices, err := repo.Devices()
	if err != nil {
		return err
	}
	device, _ := accessDeviceByID(devices, repo.DeviceID())
	roles, grants := currentDeviceAccessRoles(repo)
	environmentRoles := currentDeviceAccessRows(roles)
	payload := map[string]interface{}{
		"deviceId":         repo.DeviceID(),
		"keyPath":          repo.KeyPath(),
		"project":          repo.Manifest.ID,
		"device":           device,
		"roles":            roles,
		"environmentRoles": environmentRoles,
		"grants":           grants,
	}
	if *jsonOut {
		return printJSON(r.out, payload)
	}
	printAccessDetailRows(r.out, []accessDetailRow{
		{Label: "Device", Value: accessDeviceName(device)},
		{Label: "Device ID", Value: repo.DeviceID()},
		{Label: "Platform", Value: statusDevicePlatform(device)},
		{Label: "Status", Value: deviceStatusDisplay(device.Status)},
		{Label: "Created", Value: historyTimeDisplay(device.CreatedAt)},
		{Label: "Project", Value: repo.Manifest.ID},
		{Label: "Local key", Value: repo.KeyPath()},
	})
	fmt.Fprintln(r.out)
	printCurrentAccessRows(r.out, environmentRoles)
	return nil
}

func (r *Runner) runDeviceApprovers(args []string) error {
	fs := newFlagSet(r.deviceCommandName()+" approvers", r.errOut)
	env := fs.String("env", "", "Environment name or all")
	full := fs.Bool("full", false, "Show full device IDs")
	jsonOut := fs.Bool("json", false, "Print approvers as JSON")
	if _, err := cli.Parse(fs, args, cli.BoolFlags("full", "json")); err != nil {
		return err
	}
	repo, err := r.openRepo()
	if err != nil {
		if !isMissingLocalIdentityError(err) {
			return err
		}
		repo, err = store.OpenProject(".")
		if err != nil {
			return err
		}
	}
	approvers, err := accessApprovers(repo, *env)
	if err != nil {
		return err
	}
	if *jsonOut {
		return printJSON(r.out, map[string]interface{}{"approvers": approvers})
	}
	printAccessApproverRows(r, approvers, *full)
	return nil
}

func accessApprovers(repo store.Repository, env string) ([]store.DeviceGrant, error) {
	return repo.AccessApprovers(env)
}

func currentDeviceAccessRoles(repo store.Repository) (map[string][]string, []string) {
	matrix, err := buildAccessMatrix(repo, "")
	if err != nil {
		return map[string][]string{}, []string{}
	}
	roles := map[string][]string{}
	grants := []string{}
	for _, row := range matrix.Devices {
		if row.DeviceID != repo.DeviceID() {
			continue
		}
		for _, envName := range matrix.Environments {
			role := accessRoleDisplay(row.Roles[envName])
			if role == "-" {
				roles[envName] = []string{}
				continue
			}
			roles[envName] = accessExpandedRoles(role)
			grants = append(grants, envName)
		}
		break
	}
	return roles, grants
}

type accessEnvironmentRoleRow struct {
	Environment string   `json:"environment"`
	Role        string   `json:"role"`
	Permissions []string `json:"permissions"`
}

func currentDeviceAccessRows(roles map[string][]string) []accessEnvironmentRoleRow {
	environments := sortedRoleEnvironmentNames(roles)
	rows := make([]accessEnvironmentRoleRow, 0, len(environments))
	for _, envName := range environments {
		permissions := roles[envName]
		rows = append(rows, accessEnvironmentRoleRow{
			Environment: envName,
			Role:        accessRoleFromPermissions(permissions),
			Permissions: permissions,
		})
	}
	return rows
}

func sortedRoleEnvironmentNames(roles map[string][]string) []string {
	names := make([]string, 0, len(roles))
	for name := range roles {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func accessRoleFromPermissions(permissions []string) string {
	roleSet := map[string]bool{}
	for _, permission := range permissions {
		roleSet[permission] = true
	}
	for _, role := range []string{"owner", "grantor", "writer", "reader"} {
		if roleSet[role] {
			return role
		}
	}
	return "-"
}

func accessExpandedRoles(role string) []string {
	switch role {
	case "owner":
		return []string{"reader", "writer", "grantor", "owner"}
	case "grantor":
		return []string{"reader", "grantor"}
	case "writer":
		return []string{"reader", "writer"}
	case "reader":
		return []string{"reader"}
	default:
		return []string{}
	}
}

func (r *Runner) runDeviceGrants(args []string) error {
	fs := newFlagSet(r.deviceCommandName()+" grants", r.errOut)
	env := fs.String("env", "", "Environment name or all")
	full := fs.Bool("full", false, "Show full device IDs")
	jsonOut := fs.Bool("json", false, "Print grants as JSON")
	if _, err := cli.Parse(fs, args, cli.BoolFlags("full", "json")); err != nil {
		return err
	}
	repo, err := r.openRepo()
	if err != nil {
		return err
	}
	grants, err := repo.DeviceGrants(*env)
	if err != nil {
		return err
	}
	if *jsonOut {
		return printJSON(r.out, map[string]interface{}{"grants": grants})
	}
	printDeviceGrantRows(r, grants, *full)
	return nil
}

type accessMatrixPayload struct {
	Environments []string                `json:"environments"`
	Devices      []accessMatrixDeviceRow `json:"devices"`
}

type accessMatrixDeviceRow struct {
	DeviceID   string            `json:"deviceId"`
	DeviceName string            `json:"deviceName"`
	Platform   string            `json:"platform,omitempty"`
	Status     string            `json:"status,omitempty"`
	Current    bool              `json:"current"`
	Roles      map[string]string `json:"roles"`
}

func (r *Runner) runDeviceMatrix(args []string) error {
	fs := newFlagSet(r.deviceCommandName()+" matrix", r.errOut)
	env := fs.String("env", "", "Environment name or all")
	full := fs.Bool("full", false, "Show full device IDs")
	jsonOut := fs.Bool("json", false, "Print matrix as JSON")
	if _, err := cli.Parse(fs, args, cli.BoolFlags("full", "json")); err != nil {
		return err
	}
	repo, err := r.openRepo()
	if err != nil {
		return err
	}
	matrix, err := buildAccessMatrix(repo, *env)
	if err != nil {
		return err
	}
	if *jsonOut {
		return printJSON(r.out, matrix)
	}
	printAccessMatrixRows(r.out, matrix, *full)
	return nil
}

func (r *Runner) runDeviceRevoke(args []string) error {
	fs := newFlagSet(r.deviceCommandName()+" revoke", r.errOut)
	deviceIDFlag := fs.String("device-id", "", "Device ID to revoke")
	env := fs.String("env", "", "Environment name or all")
	assumeYes := fs.Bool("assume-yes", false, "Skip confirmation prompt")
	fs.BoolVar(assumeYes, "y", false, "Skip confirmation prompt")
	jsonOut := fs.Bool("json", false, "Print revoke result as JSON")
	positionals, err := cli.Parse(fs, args, cli.BoolFlags("assume-yes", "y", "json"))
	if err != nil {
		return err
	}
	repo, err := r.openRepo()
	if err != nil {
		return err
	}
	deviceID, err := r.selectTargetDevice(repo, *deviceIDFlag, positionals)
	if err != nil {
		return err
	}
	selectedEnv, err := r.selectShareEnvironment(repo, *env)
	if err != nil {
		return err
	}
	devices, err := repo.Devices()
	if err != nil {
		return err
	}
	deviceDisplay := accessDeviceDisplay(devices, deviceID, false)

	ok, err := r.confirm("Revoke "+deviceDisplay+" from "+selectedEnv+"?", *assumeYes)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("canceled")
	}

	result, err := repo.RevokeDevice(deviceID, selectedEnv)
	if err != nil {
		return err
	}
	if *jsonOut {
		return printJSON(r.out, result)
	}
	if !result.Revoked {
		fmt.Fprintln(r.out, warn(fmt.Sprintf("No grants found for %s in %s.", deviceDisplay, selectedEnv)))
		return nil
	}
	fmt.Fprintln(r.out, danger(fmt.Sprintf("Revoked %s from %s.", deviceDisplay, selectedEnv)))
	if len(result.Removed) > 0 {
		fmt.Fprintln(r.out)
		printDeviceGrantRows(r, result.Removed, false)
	}
	return nil
}

func (r *Runner) runDeviceDelete(args []string) error {
	fs := newFlagSet(r.deviceCommandName()+" delete", r.errOut)
	deviceIDFlag := fs.String("device-id", "", "Device ID to delete")
	assumeYes := fs.Bool("assume-yes", false, "Skip confirmation prompt")
	fs.BoolVar(assumeYes, "y", false, "Skip confirmation prompt")
	jsonOut := fs.Bool("json", false, "Print delete result as JSON")
	positionals, err := cli.Parse(fs, args, cli.BoolFlags("assume-yes", "y", "json"))
	if err != nil {
		return err
	}
	repo, err := r.openRepo()
	if err != nil {
		return err
	}
	deviceID, err := r.selectTargetDevice(repo, *deviceIDFlag, positionals)
	if err != nil {
		return err
	}
	devices, err := repo.Devices()
	if err != nil {
		return err
	}
	deviceDisplay := accessDeviceDisplay(devices, deviceID, false)

	ok, err := r.confirm("Delete "+deviceDisplay+" device record?", *assumeYes)
	if err != nil {
		return err
	}
	if !ok {
		return fmt.Errorf("canceled")
	}

	result, err := repo.DeleteDevice(deviceID)
	if err != nil {
		return err
	}
	if *jsonOut {
		return printJSON(r.out, result)
	}
	fmt.Fprintln(r.out, danger(fmt.Sprintf("Deleted %s device record.", deviceDisplay)))
	return nil
}

func (r *Runner) runDeviceShare(args []string) error {
	fs := newFlagSet(r.deviceCommandName()+" share", r.errOut)
	deviceIDFlag := fs.String("device-id", "", "Device ID to share")
	env := fs.String("env", "", "Environment name or all")
	role := fs.String("role", "", "Access role: reader, writer, grantor, owner")
	jsonOut := fs.Bool("json", false, "Print share result as JSON")
	positionals, err := cli.Parse(fs, args, cli.BoolFlags("json"))
	if err != nil {
		return err
	}
	repo, err := r.openRepo()
	if err != nil {
		return err
	}
	deviceID, err := r.selectTargetDevice(repo, *deviceIDFlag, positionals)
	if err != nil {
		return err
	}
	selectedEnv, err := r.selectShareEnvironment(repo, *env)
	if err != nil {
		return err
	}
	selectedRole, err := r.selectChoice("Select an access role", []string{"reader", "writer", "grantor", "owner"}, *role, "reader", "role")
	if err != nil {
		return err
	}
	if err := repo.ShareDevice(deviceID, selectedEnv, selectedRole); err != nil {
		return err
	}
	payload := map[string]interface{}{"deviceId": deviceID, "env": selectedEnv, "role": selectedRole, "shared": true}
	if *jsonOut {
		return printJSON(r.out, payload)
	}
	devices, err := repo.Devices()
	if err != nil {
		return err
	}
	fmt.Fprintln(r.out, success(fmt.Sprintf("Shared %s as %s for %s.", accessDeviceDisplay(devices, deviceID, false), selectedRole, selectedEnv)))
	grants, err := repo.DeviceGrants(selectedEnv)
	if err != nil {
		return err
	}
	grants = filterDeviceGrants(grants, deviceID)
	if len(grants) > 0 {
		fmt.Fprintln(r.out)
		printDeviceGrantRows(r, grants, false)
	}
	return nil
}

func (r *Runner) selectTargetDevice(repo store.Repository, provided string, positionals []string) (string, error) {
	deviceID := provided
	if len(positionals) > 0 {
		deviceID = positionals[0]
	}
	return r.selectDevice(repo, deviceID)
}

func (r *Runner) runDeviceRequests(args []string) error {
	if len(args) == 0 {
		if !r.interactive {
			r.printDeviceRequestsHelp()
			return nil
		}
		selected, err := r.selectCommand("Select an access request command", accessRequestCommandOptions)
		if err != nil {
			return err
		}
		args = append(args, selected)
	}
	if isHelpArg(args[0]) {
		r.printDeviceRequestsHelp()
		return nil
	}

	switch args[0] {
	case "list":
		return r.runDeviceRequestsList(args[1:])
	case "create":
		return r.runDeviceRequestsCreate(args[1:])
	case "approve":
		return r.runDeviceRequestsReview(args[1:], "approve")
	case "deny":
		return r.runDeviceRequestsReview(args[1:], "deny")
	default:
		return fmt.Errorf("unknown access requests command %q", args[0])
	}
}

func (r *Runner) printDeviceRequestsHelp() {
	fmt.Fprintf(r.out, "Usage: ghostable %s requests <list|create|approve|deny> [options]\n", r.deviceCommandName())
	fmt.Fprintln(r.out)
	fmt.Fprintln(r.out, warn("Commands:"))
	printCommandDescriptions(r.out, accessRequestCommandOptions)
}

func (r *Runner) runDeviceRequestsList(args []string) error {
	fs := newFlagSet(r.deviceCommandName()+" requests list", r.errOut)
	all := fs.Bool("all", false, "Include reviewed and already granted requests")
	jsonOut := fs.Bool("json", false, "Print access requests as JSON")
	if _, err := cli.Parse(fs, args, cli.BoolFlags("all", "json")); err != nil {
		return err
	}
	repo, err := store.OpenProject(".")
	if err != nil {
		return err
	}
	requests, err := repo.ListAccessRequests(*all)
	if err != nil {
		return err
	}
	if *jsonOut {
		return printJSON(r.out, requests)
	}
	if r.interactive && len(requests.Valid) > 0 {
		reviewRepo, err := r.openRepo()
		if err != nil {
			printAccessRequestRows(r.out, requests.Valid, false)
			return err
		}
		return r.reviewAccessRequestFromList(reviewRepo, requests.Valid)
	}
	printAccessRequestRows(r.out, requests.Valid, false)
	if len(requests.Invalid) > 0 {
		fmt.Fprintln(r.out)
		fmt.Fprintln(r.out, warn("Ignored requests:"))
		for _, entry := range requests.Invalid {
			fmt.Fprintf(r.out, "%s  %s\n", accessDeviceID(entry.Request.ID, false), danger(entry.Error))
		}
	}
	return nil
}

func (r *Runner) reviewAccessRequestFromList(repo store.Repository, requests []store.AccessRequestEntry) error {
	options := accessRequestSelectOptions(requests, repo.DeviceID(), true)
	if len(options) == 0 {
		fmt.Fprintln(r.out, warn("No pending access requests."))
		return nil
	}
	requestID, err := r.prompts.SelectOptions("Select an access request", options, 0)
	if err != nil {
		return err
	}
	entry, ok := accessRequestEntryByID(requests, requestID)
	if !ok {
		return fmt.Errorf("access request %q was not found", requestID)
	}
	if entry.Request.DeviceID == repo.DeviceID() {
		fmt.Fprintln(r.out, warn("This request was created by this device. Another device with grant access needs to approve or deny it."))
		return nil
	}
	action, err := r.prompts.Select("Review request", []string{"approve", "deny", "cancel"}, 0)
	if err != nil {
		return err
	}
	if action == "cancel" {
		return nil
	}
	reason := ""
	if action == "deny" {
		reason, err = r.askOptional("Reason", "")
		if err != nil {
			return err
		}
	}
	result, err := r.reviewAccessRequest(repo, requestID, action, reason)
	if err != nil {
		return err
	}
	printAccessRequestReviewResult(r.out, result)
	return nil
}

func (r *Runner) runDeviceRequestsCreate(args []string) error {
	fs := newFlagSet(r.deviceCommandName()+" requests create", r.errOut)
	env := fs.String("env", "", "Environment name or all")
	role := fs.String("role", "", "Requested role: reader, writer, grantor, owner")
	reason := fs.String("reason", "", "Reason for the request")
	jsonOut := fs.Bool("json", false, "Print access request as JSON")
	if _, err := cli.Parse(fs, args, cli.BoolFlags("json")); err != nil {
		return err
	}
	repo, err := r.openRepo()
	if err != nil {
		return err
	}
	selectedEnv, err := r.selectShareEnvironment(repo, *env)
	if err != nil {
		return err
	}
	selectedRole, err := r.selectChoice("Select requested access", []string{"reader", "writer", "grantor", "owner"}, *role, "reader", "role")
	if err != nil {
		return err
	}
	requestReason, err := r.askOptional("Reason", *reason)
	if err != nil {
		return err
	}
	result, err := repo.CreateAccessRequest(selectedEnv, selectedRole, requestReason)
	if err != nil {
		return err
	}
	if *jsonOut {
		return printJSON(r.out, result)
	}
	switch result.State {
	case "already_granted":
		fmt.Fprintln(r.out, success(fmt.Sprintf("This device already has %s access for %s.", result.Role, result.Environment)))
	case "existing":
		fmt.Fprintln(r.out, warn(fmt.Sprintf("Access request %s already exists.", accessDeviceID(result.RequestID, false))))
	default:
		fmt.Fprintln(r.out, success(fmt.Sprintf("Created access request %s.", accessDeviceID(result.RequestID, false))))
	}
	printAccessDetailRows(r.out, []accessDetailRow{
		{Label: "Device", Value: accessDeviceDisplayDevice(result.Device, false)},
		{Label: "Environment", Value: result.Environment},
		{Label: "Role", Value: result.Role},
		{Label: "Request file", Value: result.RequestPath},
	})
	return nil
}

func (r *Runner) runDeviceRequestsReview(args []string, action string) error {
	fs := newFlagSet(r.deviceCommandName()+" requests "+action, r.errOut)
	requestIDFlag := fs.String("request-id", "", "Request ID to review")
	reason := fs.String("reason", "", "Review reason")
	jsonOut := fs.Bool("json", false, "Print review result as JSON")
	positionals, err := cli.Parse(fs, args, cli.BoolFlags("json"))
	if err != nil {
		return err
	}
	requestID := *requestIDFlag
	if len(positionals) > 0 {
		requestID = positionals[0]
	}
	repo, err := r.openRepo()
	if err != nil {
		return err
	}
	requestID, err = r.selectAccessRequest(repo, requestID, action)
	if err != nil {
		return err
	}
	reviewReason := *reason
	if action == "deny" {
		reviewReason, err = r.askOptional("Reason", *reason)
		if err != nil {
			return err
		}
	}
	result, err := r.reviewAccessRequest(repo, requestID, action, reviewReason)
	if err != nil {
		return err
	}
	if *jsonOut {
		return printJSON(r.out, result)
	}
	printAccessRequestReviewResult(r.out, result)
	return nil
}

func (r *Runner) reviewAccessRequest(repo store.Repository, requestID string, action string, reason string) (store.AccessRequestReviewResult, error) {
	if action == "approve" {
		return repo.ApproveAccessRequest(requestID)
	}
	return repo.DenyAccessRequest(requestID, reason)
}

func printAccessRequestReviewResult(out io.Writer, result store.AccessRequestReviewResult) {
	if result.State == "approved" {
		fmt.Fprintln(out, success(fmt.Sprintf("Approved access request %s.", accessDeviceID(result.RequestID, false))))
	} else {
		fmt.Fprintln(out, danger(fmt.Sprintf("Denied access request %s.", accessDeviceID(result.RequestID, false))))
	}
	printAccessDetailRows(out, []accessDetailRow{
		{Label: "Device ID", Value: result.DeviceID},
		{Label: "Environment", Value: result.Environment},
		{Label: "Role", Value: result.Role},
		{Label: "State", Value: result.State},
	})
}

func (r *Runner) selectAccessRequest(repo store.Repository, provided string, action string) (string, error) {
	if strings.TrimSpace(provided) != "" {
		return strings.TrimSpace(provided), nil
	}
	if !r.interactive {
		return "", fmt.Errorf("pass --request-id")
	}

	requests, err := repo.ListAccessRequests(false)
	if err != nil {
		return "", err
	}
	options := accessRequestSelectOptions(requests.Valid, repo.DeviceID(), false)
	if len(options) == 0 {
		if hasOwnPendingAccessRequests(requests.Valid, repo.DeviceID()) {
			return "", fmt.Errorf("only pending requests from this device are available; another device with grant access must approve or deny them")
		}
		return "", fmt.Errorf("no pending access requests from other devices are available to %s", action)
	}
	return r.prompts.SelectOptions("Select access request to "+action, options, 0)
}

func accessRequestSelectOptions(requests []store.AccessRequestEntry, currentDeviceID string, includeOwn bool) []prompt.SelectOption {
	options := []prompt.SelectOption{}
	for _, entry := range requests {
		own := entry.Request.DeviceID == currentDeviceID
		if own && !includeOwn {
			continue
		}
		label := accessDeviceID(entry.Request.ID, false)
		if own {
			label += " (your request)"
		}
		options = append(options, prompt.SelectOption{
			Label:       label,
			Value:       entry.Request.ID,
			Description: accessRequestOptionDescription(entry, own),
		})
	}
	return options
}

func accessRequestEntryByID(requests []store.AccessRequestEntry, requestID string) (store.AccessRequestEntry, bool) {
	for _, entry := range requests {
		if entry.Request.ID == requestID {
			return entry, true
		}
	}
	return store.AccessRequestEntry{}, false
}

func hasOwnPendingAccessRequests(requests []store.AccessRequestEntry, currentDeviceID string) bool {
	for _, entry := range requests {
		if entry.Request.DeviceID == currentDeviceID {
			return true
		}
	}
	return false
}

func accessRequestOptionDescription(entry store.AccessRequestEntry, own bool) string {
	env := entry.Request.Environment
	if strings.TrimSpace(env) == "" {
		env = "all"
	}
	description := fmt.Sprintf("%s requested %s for %s", accessDeviceName(entry.Device), entry.Request.Role, env)
	if own {
		description += "; another device must review it"
	}
	return description
}

func (r *Runner) selectShareEnvironment(repo store.Repository, provided string) (string, error) {
	if provided != "" {
		return provided, nil
	}
	if !r.interactive {
		return "all", nil
	}

	envs := repo.Environments()
	choices := make([]string, 0, len(envs)+1)
	choices = append(choices, "all")
	for _, env := range envs {
		choices = append(choices, env.Name)
	}
	return r.prompts.Select("Select an environment", choices, 0)
}

func printDeviceGrantRows(r *Runner, grants []store.DeviceGrant, full bool) {
	if len(grants) == 0 {
		fmt.Fprintln(r.out, warn("No device grants found."))
		return
	}

	envWidth := len("Environment")
	roleWidth := len("Role")
	deviceWidth := len("Device")
	platformWidth := len("Platform")
	statusWidth := len("Status")
	for _, grant := range grants {
		envWidth = max(envWidth, len(grant.Environment))
		roleWidth = max(roleWidth, len(grant.Role))
		deviceWidth = max(deviceWidth, len(terminalSafeText(grant.DeviceName)))
		platformWidth = max(platformWidth, len(statusGrantPlatform(grant)))
		statusWidth = max(statusWidth, len(deviceStatusDisplay(grant.Status)))
	}

	header := fmt.Sprintf("%-*s  %-*s  %-*s  %-*s  %-*s  %-7s  %s", envWidth, "Environment", roleWidth, "Role", deviceWidth, "Device", platformWidth, "Platform", statusWidth, "Status", "Current", "ID")
	fmt.Fprintln(r.out, warn(header))
	fmt.Fprintf(r.out, "%-*s  %-*s  %-*s  %-*s  %-*s  %-7s  %s\n",
		envWidth,
		strings.Repeat("-", envWidth),
		roleWidth,
		strings.Repeat("-", roleWidth),
		deviceWidth,
		strings.Repeat("-", deviceWidth),
		platformWidth,
		strings.Repeat("-", platformWidth),
		statusWidth,
		strings.Repeat("-", statusWidth),
		"-------",
		"--",
	)
	for _, grant := range grants {
		current := "-"
		if grant.Current {
			current = "yes"
		}
		id := grant.DeviceID
		if !full {
			id = statusShortID(id)
		}
		status := deviceStatusDisplay(grant.Status)
		fmt.Fprintf(r.out, "%s  %s  %-*s  %-*s  %s  %s  %s\n",
			coloredCell(grant.Environment, envWidth, success),
			coloredCell(grant.Role, roleWidth, success),
			deviceWidth,
			terminalSafeText(grant.DeviceName),
			platformWidth,
			statusGrantPlatform(grant),
			coloredCell(status, statusWidth, deviceStatusColor),
			coloredCell(current, 7, currentColor),
			id,
		)
	}
}

func printAccessApproverRows(r *Runner, approvers []store.DeviceGrant, full bool) {
	if len(approvers) == 0 {
		fmt.Fprintln(r.out, warn("No devices can grant access."))
		return
	}
	printDeviceGrantRows(r, approvers, full)
}

func printAccessRequestRows(out io.Writer, requests []store.AccessRequestEntry, full bool) {
	if len(requests) == 0 {
		fmt.Fprintln(out, warn("No pending access requests."))
		return
	}

	requestWidth := len("Request")
	deviceWidth := len("Device")
	envWidth := len("Environment")
	roleWidth := len("Role")
	stateWidth := len("State")
	createdWidth := len("Created")
	for _, entry := range requests {
		requestWidth = max(requestWidth, len(accessDeviceID(entry.Request.ID, full)))
		deviceWidth = max(deviceWidth, len(accessDeviceName(entry.Device)))
		envWidth = max(envWidth, len(entry.Request.Environment))
		roleWidth = max(roleWidth, len(entry.Request.Role))
		stateWidth = max(stateWidth, len(entry.AccessState))
		createdWidth = max(createdWidth, len(historyTimeDisplay(entry.Request.CreatedAt)))
	}

	header := fmt.Sprintf("%-*s  %-*s  %-*s  %-*s  %-*s  %s", requestWidth, "Request", deviceWidth, "Device", envWidth, "Environment", roleWidth, "Role", stateWidth, "State", "Created")
	fmt.Fprintln(out, warn(header))
	fmt.Fprintf(out, "%-*s  %-*s  %-*s  %-*s  %-*s  %s\n",
		requestWidth,
		strings.Repeat("-", requestWidth),
		deviceWidth,
		strings.Repeat("-", deviceWidth),
		envWidth,
		strings.Repeat("-", envWidth),
		roleWidth,
		strings.Repeat("-", roleWidth),
		stateWidth,
		strings.Repeat("-", stateWidth),
		strings.Repeat("-", createdWidth),
	)
	for _, entry := range requests {
		fmt.Fprintf(out, "%-*s  %s  %s  %s  %s  %-*s\n",
			requestWidth,
			accessDeviceID(entry.Request.ID, full),
			coloredCell(accessDeviceName(entry.Device), deviceWidth, success),
			coloredCell(entry.Request.Environment, envWidth, success),
			coloredCell(entry.Request.Role, roleWidth, success),
			coloredCell(entry.AccessState, stateWidth, accessRequestStateColor),
			createdWidth,
			historyTimeDisplay(entry.Request.CreatedAt),
		)
	}
}

func accessRequestStateColor(value string) string {
	switch value {
	case "approved", "granted":
		return success(value)
	case "denied":
		return danger(value)
	case "pending":
		return warn(value)
	default:
		return value
	}
}

func buildAccessMatrix(repo store.Repository, env string) (accessMatrixPayload, error) {
	envNames, err := accessMatrixEnvironmentNames(repo, env)
	if err != nil {
		return accessMatrixPayload{}, err
	}
	devices, err := repo.Devices()
	if err != nil {
		return accessMatrixPayload{}, err
	}
	grants, err := repo.DeviceGrants(env)
	if err != nil {
		return accessMatrixPayload{}, err
	}

	rolesByDevice := map[string]map[string]string{}
	for _, grant := range grants {
		if _, ok := rolesByDevice[grant.DeviceID]; !ok {
			rolesByDevice[grant.DeviceID] = map[string]string{}
		}
		rolesByDevice[grant.DeviceID][grant.Environment] = grant.Role
	}

	rows := make([]accessMatrixDeviceRow, 0, len(devices))
	for _, device := range devices {
		roles := map[string]string{}
		for _, envName := range envNames {
			roles[envName] = rolesByDevice[device.ID][envName]
		}
		rows = append(rows, accessMatrixDeviceRow{
			DeviceID:   device.ID,
			DeviceName: accessDeviceName(device),
			Platform:   device.Platform,
			Status:     device.Status,
			Current:    device.ID == repo.DeviceID(),
			Roles:      roles,
		})
	}

	return accessMatrixPayload{Environments: envNames, Devices: rows}, nil
}

func accessMatrixEnvironmentNames(repo store.Repository, env string) ([]string, error) {
	if strings.TrimSpace(env) != "" && env != "all" {
		if _, ok := repo.Manifest.Environments[env]; !ok {
			return nil, fmt.Errorf("environment %q was not found", env)
		}
		return []string{env}, nil
	}

	envs := repo.Environments()
	names := make([]string, 0, len(envs))
	for _, environment := range envs {
		names = append(names, environment.Name)
	}
	return names, nil
}

func printAccessMatrixRows(out io.Writer, matrix accessMatrixPayload, full bool) {
	if len(matrix.Devices) == 0 {
		fmt.Fprintln(out, warn("No devices found."))
		return
	}

	deviceWidth := len("Device")
	platformWidth := len("Platform")
	statusWidth := len("Status")
	idWidth := len("ID")
	envWidths := make(map[string]int, len(matrix.Environments))
	for _, envName := range matrix.Environments {
		envWidths[envName] = len(envName)
	}
	for _, row := range matrix.Devices {
		deviceWidth = max(deviceWidth, len(row.DeviceName))
		platformWidth = max(platformWidth, len(statusDevicePlatform(domain.DeviceRecord{Platform: row.Platform})))
		statusWidth = max(statusWidth, len(deviceStatusDisplay(row.Status)))
		idWidth = max(idWidth, len(accessDeviceID(row.DeviceID, full)))
		for _, envName := range matrix.Environments {
			envWidths[envName] = max(envWidths[envName], len(accessRoleDisplay(row.Roles[envName])))
		}
	}

	headerParts := []string{
		fmt.Sprintf("%-*s", deviceWidth, "Device"),
		fmt.Sprintf("%-*s", platformWidth, "Platform"),
		fmt.Sprintf("%-*s", statusWidth, "Status"),
		fmt.Sprintf("%-7s", "Current"),
		fmt.Sprintf("%-*s", idWidth, "ID"),
	}
	for _, envName := range matrix.Environments {
		headerParts = append(headerParts, fmt.Sprintf("%-*s", envWidths[envName], envName))
	}
	fmt.Fprintln(out, warn(strings.Join(headerParts, "  ")))

	dividerParts := []string{
		strings.Repeat("-", deviceWidth),
		strings.Repeat("-", platformWidth),
		strings.Repeat("-", statusWidth),
		"-------",
		strings.Repeat("-", idWidth),
	}
	for _, envName := range matrix.Environments {
		dividerParts = append(dividerParts, strings.Repeat("-", envWidths[envName]))
	}
	fmt.Fprintln(out, strings.Join(dividerParts, "  "))

	for _, row := range matrix.Devices {
		current := "-"
		if row.Current {
			current = "yes"
		}
		cells := []string{
			coloredCell(row.DeviceName, deviceWidth, success),
			fmt.Sprintf("%-*s", platformWidth, statusDevicePlatform(domain.DeviceRecord{Platform: row.Platform})),
			coloredCell(deviceStatusDisplay(row.Status), statusWidth, deviceStatusColor),
			coloredCell(current, 7, currentColor),
			fmt.Sprintf("%-*s", idWidth, accessDeviceID(row.DeviceID, full)),
		}
		for _, envName := range matrix.Environments {
			role := accessRoleDisplay(row.Roles[envName])
			cells = append(cells, coloredCell(role, envWidths[envName], accessRoleColor))
		}
		fmt.Fprintln(out, strings.Join(cells, "  "))
	}
}

func accessRoleDisplay(role string) string {
	if strings.TrimSpace(role) == "" {
		return "-"
	}
	return role
}

func accessRoleColor(role string) string {
	if role == "-" {
		return role
	}
	return success(role)
}

func printCurrentAccessRows(out io.Writer, rows []accessEnvironmentRoleRow) {
	if len(rows) == 0 {
		fmt.Fprintln(out, warn("No environment access found for this device."))
		return
	}

	envWidth := len("Environment")
	roleWidth := len("Role")
	permissionsWidth := len("Permissions")
	for _, row := range rows {
		envWidth = max(envWidth, len(row.Environment))
		roleWidth = max(roleWidth, len(row.Role))
		permissionsWidth = max(permissionsWidth, len(accessPermissionsDisplay(row.Permissions)))
	}

	header := fmt.Sprintf("%-*s  %-*s  %s", envWidth, "Environment", roleWidth, "Role", "Permissions")
	fmt.Fprintln(out, warn(header))
	fmt.Fprintf(out, "%-*s  %-*s  %s\n", envWidth, strings.Repeat("-", envWidth), roleWidth, strings.Repeat("-", roleWidth), strings.Repeat("-", permissionsWidth))
	for _, row := range rows {
		fmt.Fprintln(out, strings.Join([]string{
			coloredCell(row.Environment, envWidth, success),
			coloredCell(row.Role, roleWidth, accessRoleColor),
			accessPermissionsDisplay(row.Permissions),
		}, "  "))
	}
}

func accessPermissionsDisplay(permissions []string) string {
	if len(permissions) == 0 {
		return "-"
	}
	labels := make([]string, 0, len(permissions))
	for _, permission := range permissions {
		switch permission {
		case "reader":
			labels = append(labels, "read")
		case "writer":
			labels = append(labels, "write")
		case "grantor":
			labels = append(labels, "grant")
		case "owner":
			labels = append(labels, "own")
		default:
			labels = append(labels, permission)
		}
	}
	return strings.Join(labels, ", ")
}

type accessDetailRow struct {
	Label string
	Value string
}

func printAccessDetailRows(out io.Writer, rows []accessDetailRow) {
	labelWidth := len("Field")
	valueWidth := len("Value")
	for _, row := range rows {
		labelWidth = max(labelWidth, len(row.Label))
		valueWidth = max(valueWidth, len(row.Value))
	}

	header := fmt.Sprintf("%-*s  %s", labelWidth, "Field", "Value")
	fmt.Fprintln(out, warn(header))
	fmt.Fprintf(out, "%-*s  %s\n", labelWidth, strings.Repeat("-", labelWidth), strings.Repeat("-", valueWidth))
	for _, row := range rows {
		fmt.Fprintf(out, "%-*s  %s\n", labelWidth, row.Label, success(row.Value))
	}
}

func printAccessDeviceRows(out io.Writer, devices []domain.DeviceRecord, currentDeviceID string, full bool) {
	if len(devices) == 0 {
		fmt.Fprintln(out, warn("No devices found."))
		return
	}

	deviceWidth := len("Device")
	platformWidth := len("Platform")
	statusWidth := len("Status")
	createdWidth := len("Created")
	idWidth := len("ID")
	for _, device := range devices {
		deviceWidth = max(deviceWidth, len(accessDeviceName(device)))
		platformWidth = max(platformWidth, len(statusDevicePlatform(device)))
		statusWidth = max(statusWidth, len(deviceStatusDisplay(device.Status)))
		createdWidth = max(createdWidth, len(historyTimeDisplay(device.CreatedAt)))
		idWidth = max(idWidth, len(accessDeviceID(device.ID, full)))
	}

	header := fmt.Sprintf("%-*s  %-*s  %-*s  %-7s  %-*s  %s", deviceWidth, "Device", platformWidth, "Platform", statusWidth, "Status", "Current", createdWidth, "Created", "ID")
	fmt.Fprintln(out, warn(header))
	fmt.Fprintf(out, "%-*s  %-*s  %-*s  %-7s  %-*s  %s\n",
		deviceWidth,
		strings.Repeat("-", deviceWidth),
		platformWidth,
		strings.Repeat("-", platformWidth),
		statusWidth,
		strings.Repeat("-", statusWidth),
		"-------",
		createdWidth,
		strings.Repeat("-", createdWidth),
		strings.Repeat("-", idWidth),
	)
	for _, device := range devices {
		current := "-"
		if device.ID == currentDeviceID {
			current = "yes"
		}
		status := deviceStatusDisplay(device.Status)
		fmt.Fprintf(out, "%s  %-*s  %s  %s  %-*s  %s\n",
			coloredCell(accessDeviceName(device), deviceWidth, success),
			platformWidth,
			statusDevicePlatform(device),
			coloredCell(status, statusWidth, deviceStatusColor),
			coloredCell(current, 7, currentColor),
			createdWidth,
			historyTimeDisplay(device.CreatedAt),
			accessDeviceID(device.ID, full),
		)
	}
}

func coloredCell(value string, width int, color func(string) string) string {
	value = terminalSafeText(value)
	return color(value) + strings.Repeat(" ", max(0, width-len(value)))
}

func currentColor(value string) string {
	if value == "yes" {
		return success(value)
	}
	return value
}

func deviceStatusDisplay(value string) string {
	value = strings.TrimSpace(terminalSafeText(value))
	if strings.TrimSpace(value) == "" {
		return "-"
	}
	return value
}

func deviceStatusColor(value string) string {
	if strings.EqualFold(value, "active") {
		return success(value)
	}
	if value == "-" {
		return value
	}
	return danger(value)
}

func accessDeviceID(id string, full bool) string {
	if full {
		return id
	}
	return statusShortID(id)
}

func accessDeviceName(device domain.DeviceRecord) string {
	if device.ID == "" && strings.TrimSpace(device.Name) == "" {
		return "Unknown device"
	}
	return statusDeviceName(device)
}

func accessDeviceDisplay(devices []domain.DeviceRecord, deviceID string, full bool) string {
	device, ok := accessDeviceByID(devices, deviceID)
	if !ok {
		return accessDeviceID(deviceID, full)
	}
	return accessDeviceDisplayDevice(device, full)
}

func accessDeviceDisplayDevice(device domain.DeviceRecord, full bool) string {
	id := accessDeviceID(device.ID, full)
	if id == "" {
		return accessDeviceName(device)
	}
	return fmt.Sprintf("%s (%s)", accessDeviceName(device), id)
}

func accessDeviceByID(devices []domain.DeviceRecord, deviceID string) (domain.DeviceRecord, bool) {
	for _, device := range devices {
		if device.ID == deviceID {
			return device, true
		}
	}
	return domain.DeviceRecord{}, false
}

func filterDeviceGrants(grants []store.DeviceGrant, deviceID string) []store.DeviceGrant {
	filtered := make([]store.DeviceGrant, 0, len(grants))
	for _, grant := range grants {
		if grant.DeviceID == deviceID {
			filtered = append(filtered, grant)
		}
	}
	return filtered
}

func statusGrantPlatform(grant store.DeviceGrant) string {
	platform := strings.TrimSpace(terminalSafeText(grant.Platform))
	if platform == "" {
		return "-"
	}
	return platform
}
