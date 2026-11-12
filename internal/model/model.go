// Package model holds the Terraform state and plan models of the provider's
// resources and data sources.
//
// Attribute names are the beenfar API's field names, so the name of an
// attribute is also its update_mask path. The `beenfar` struct tag says how
// an attribute takes part in partial management (see package provider):
//
//	(none)      optional and managed only when set in the configuration
//	required    always managed
//	computed    reported by the controller; never sent
//	secret      write-only: never returned by the controller
//	local       provider-only; never sent or read back
package model

import "github.com/hashicorp/terraform-plugin-framework/types"

type Network struct {
	ID          types.String `tfsdk:"id" beenfar:"computed"`
	Name        types.String `tfsdk:"name" beenfar:"required"`
	VlanID      types.Int64  `tfsdk:"vlan_id"`
	Subnet      types.String `tfsdk:"subnet"`
	DhcpEnabled types.Bool   `tfsdk:"dhcp_enabled"`
	DhcpStart   types.String `tfsdk:"dhcp_start"`
	DhcpStop    types.String `tfsdk:"dhcp_stop"`
	DomainName  types.String `tfsdk:"domain_name"`
}

type WifiNetwork struct {
	ID               types.String `tfsdk:"id" beenfar:"computed"`
	Ssid             types.String `tfsdk:"ssid" beenfar:"required"`
	Security         types.String `tfsdk:"security" beenfar:"required"`
	SecurityKey      types.String `tfsdk:"security_key"`
	Band             types.String `tfsdk:"band"`
	NetworkID        types.String `tfsdk:"network_id"`
	Guest            types.Bool   `tfsdk:"guest"`
	Hidden           types.Bool   `tfsdk:"hidden"`
	ApGroups         types.Set    `tfsdk:"ap_groups"`
	DefaultUserGroup types.String `tfsdk:"default_user_group"`
	RadiusServerID   types.String `tfsdk:"radius_server_id"`
}

// WifiNetworkAPGroups is the ap_groups field of a wifi network, managed on
// its own.
type WifiNetworkAPGroups struct {
	ID            types.String `tfsdk:"id" beenfar:"computed"`
	WifiNetworkID types.String `tfsdk:"wifi_network_id" beenfar:"local"`
	ApGroups      types.Set    `tfsdk:"ap_groups" beenfar:"required"`
}

type RadiusServer struct {
	ID                  types.String `tfsdk:"id" beenfar:"computed"`
	Name                types.String `tfsdk:"name" beenfar:"required"`
	Address             types.String `tfsdk:"address" beenfar:"required"`
	AuthPort            types.Int64  `tfsdk:"auth_port"`
	AccountingPort      types.Int64  `tfsdk:"accounting_port"`
	SharedSecret        types.String `tfsdk:"shared_secret" beenfar:"secret"`
	SharedSecretVersion types.Int64  `tfsdk:"shared_secret_version" beenfar:"computed"`
}

type SwitchPort struct {
	ID               types.String `tfsdk:"id" beenfar:"computed"`
	DeviceID         types.String `tfsdk:"device_id" beenfar:"required"`
	PortIndex        types.Int64  `tfsdk:"port_index" beenfar:"required"`
	Name             types.String `tfsdk:"name"`
	Disabled         types.Bool   `tfsdk:"disabled"`
	NativeNetworkID  types.String `tfsdk:"native_network_id"`
	Tagging          types.String `tfsdk:"tagging"`
	TaggedNetworkIds types.Set    `tfsdk:"tagged_network_ids"`
	PoeMode          types.String `tfsdk:"poe_mode"`
}

// Device is a device as the data sources report it.
type Device struct {
	ID         types.String `tfsdk:"id" beenfar:"computed"`
	Mac        types.String `tfsdk:"mac" beenfar:"computed"`
	Adapter    types.String `tfsdk:"adapter" beenfar:"computed"`
	State      types.String `tfsdk:"state" beenfar:"computed"`
	Model      types.String `tfsdk:"model" beenfar:"computed"`
	IP         types.String `tfsdk:"ip" beenfar:"computed"`
	Firmware   types.String `tfsdk:"firmware" beenfar:"computed"`
	Locked     types.Bool   `tfsdk:"locked" beenfar:"computed"`
	Readoption types.String `tfsdk:"readoption" beenfar:"computed"`
}

// AdoptedDevice is the beenfar_device resource: a device adopted by MAC.
type AdoptedDevice struct {
	ID            types.String `tfsdk:"id" beenfar:"computed"`
	Mac           types.String `tfsdk:"mac" beenfar:"required"`
	WaitForDevice types.String `tfsdk:"wait_for_device" beenfar:"local"`
	Adapter       types.String `tfsdk:"adapter" beenfar:"computed"`
	Model         types.String `tfsdk:"model" beenfar:"computed"`
	IP            types.String `tfsdk:"ip" beenfar:"computed"`
	Firmware      types.String `tfsdk:"firmware" beenfar:"computed"`
}

type Adapter struct {
	Name          types.String `tfsdk:"name"`
	Kind          types.String `tfsdk:"kind"`
	Version       types.String `tfsdk:"version"`
	Healthy       types.Bool   `tfsdk:"healthy"`
	StatusMessage types.String `tfsdk:"status_message"`
}
