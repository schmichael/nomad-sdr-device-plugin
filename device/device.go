package device

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	log "github.com/hashicorp/go-hclog"
	"github.com/hashicorp/nomad/plugins/base"
	"github.com/hashicorp/nomad/plugins/device"
	"github.com/hashicorp/nomad/plugins/shared/hclspec"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const (
	// pluginName is the deviceName of the plugin
	// this is used for logging and (along with the version) for uniquely identifying
	// plugin binaries fingerprinted by the client
	pluginName = "rtl2838-device"

	// plugin version allows the client to identify and use newer versions of
	// an installed plugin
	pluginVersion = "v0.1.0"

	// vendor is the label for the vendor providing the devices.
	// along with "type" and "model", this can be used when requesting devices:
	//   https://www.nomadproject.io/docs/job-specification/device.html#name
	vendor = "realtek"

	// deviceType is the "type" of device being returned
	deviceType = "sdr"
)

var (
	// pluginInfo provides information used by Nomad to identify the plugin
	pluginInfo = &base.PluginInfoResponse{
		Type:              base.PluginTypeDevice,
		PluginApiVersions: []string{device.ApiVersion010},
		PluginVersion:     pluginVersion,
		Name:              pluginName,
	}

	// configSpec is the specification of the schema for this plugin's config.
	// this is used to validate the HCL for the plugin provided
	// as part of the client config:
	//   https://www.nomadproject.io/docs/configuration/plugin.html
	// options are here:
	//   https://github.com/hashicorp/nomad/blob/v0.10.0/plugins/shared/hclspec/hcl_spec.proto
	configSpec = hclspec.NewObject(map[string]*hclspec.Spec{
		"fingerprint_period": hclspec.NewDefault(
			hclspec.NewAttr("fingerprint_period", "string", false),
			hclspec.NewLiteral("\"1m\""),
		),
	})
)

// Config contains configuration information for the plugin.
type Config struct {
	FingerprintPeriod string `codec:"fingerprint_period"`
}

// RTL2838DevicePlugin is a device plugin for Realtek RTL2838 DVB-T software
// defined radio (SDR).
type RTL2838DevicePlugin struct {
	logger log.Logger

	// fingerprintPeriod the period for the fingerprinting loop
	// most plugins that fingerprint in a polling loop will have this
	fingerprintPeriod time.Duration

	// devices is a list of fingerprinted devices
	// most plugins will maintain, at least, a list of the devices that were
	// discovered during fingerprinting.
	// we'll save the "device name"/"model"
	devices    map[string]*fingerprintedDevice
	deviceLock sync.RWMutex
}

// NewPlugin returns a device plugin, used primarily by the main wrapper
//
// Plugin configuration isn't available yet, so there will typically be
// a limit to the initialization that can be performed at this point.
func NewPlugin(log log.Logger) *RTL2838DevicePlugin {
	return &RTL2838DevicePlugin{
		logger:  log.Named(pluginName),
		devices: make(map[string]*fingerprintedDevice),
	}
}

// PluginInfo returns information describing the plugin.
//
// This is called during Nomad client startup, while discovering and loading
// plugins.
func (d *RTL2838DevicePlugin) PluginInfo() (*base.PluginInfoResponse, error) {
	return pluginInfo, nil
}

// ConfigSchema returns the configuration schema for the plugin.
//
// This is called during Nomad client startup, immediately before parsing
// plugin config and calling SetConfig
func (d *RTL2838DevicePlugin) ConfigSchema() (*hclspec.Spec, error) {
	return configSpec, nil
}

// SetConfig is called by the client to pass the configuration for the plugin.
func (d *RTL2838DevicePlugin) SetConfig(c *base.Config) error {

	// decode the plugin config
	var config Config
	if err := base.MsgPackDecode(c.PluginConfig, &config); err != nil {
		return err
	}

	// for example, convert the poll period from an HCL string into a time.Duration
	period, err := time.ParseDuration(config.FingerprintPeriod)
	if err != nil {
		return fmt.Errorf("failed to parse doFingerprint period %q: %v", config.FingerprintPeriod, err)
	}
	d.fingerprintPeriod = period

	return nil
}

// Fingerprint streams detected devices.
// Messages should be emitted to the returned channel when there are changes
// to the devices or their health.
func (d *RTL2838DevicePlugin) Fingerprint(ctx context.Context) (<-chan *device.FingerprintResponse, error) {
	// Fingerprint returns a channel. The recommended way of organizing a plugin
	// is to pass that into a long-running goroutine and return the channel immediately.
	outCh := make(chan *device.FingerprintResponse)
	go d.doFingerprint(ctx, outCh)
	return outCh, nil
}

// Stats streams statistics for the detected devices.
// Messages should be emitted to the returned channel on the specified interval.
func (d *RTL2838DevicePlugin) Stats(ctx context.Context, interval time.Duration) (<-chan *device.StatsResponse, error) {
	// Similar to Fingerprint, Stats returns a channel. The recommended way of
	// organizing a plugin is to pass that into a long-running goroutine and
	// return the channel immediately.
	outCh := make(chan *device.StatsResponse)
	go d.doStats(ctx, outCh, interval)
	return outCh, nil
}

type reservationError struct {
	notExistingIDs []string
}

func (e *reservationError) Error() string {
	return fmt.Sprintf("unknown device IDs: %s", strings.Join(e.notExistingIDs, ","))
}

// Reserve returns information to the task driver on on how to mount the given devices.
// It may also perform any device-specific orchestration necessary to prepare the device
// for use. This is called in a pre-start hook on the client, before starting the workload.
func (d *RTL2838DevicePlugin) Reserve(deviceIDs []string) (*device.ContainerReservation, error) {
	if len(deviceIDs) == 0 {
		return &device.ContainerReservation{}, nil
	}

	// This pattern can be useful for some drivers to avoid a race condition where a device disappears
	// after being scheduled by the server but before the server gets an update on the fingerprint
	// channel that the device is no longer available.
	d.deviceLock.RLock()
	var notExistingIDs []string
	for _, id := range deviceIDs {
		if _, deviceIDExists := d.devices[id]; !deviceIDExists {
			notExistingIDs = append(notExistingIDs, id)
		}
	}
	d.deviceLock.RUnlock()

	if len(notExistingIDs) != 0 {
		return nil, &reservationError{notExistingIDs}
	}

	// initialize the response
	resp := &device.ContainerReservation{
		Envs:    map[string]string{},
		Mounts:  []*device.Mount{},
		Devices: []*device.DeviceSpec{},
	}

	//TODO libusb?
	//resp.Mounts = append(resp.Mounts, &device.Mount{})

	for i, id := range deviceIDs {
		// Check if the device is known
		dev, ok := d.devices[id]
		if !ok {
			return nil, status.Newf(codes.InvalidArgument, "unknown device %q", id).Err()
		}

		// /dev/bus/usb/${bus}/${dev} -> /dev/bus/usb/001/007
		path := fmt.Sprintf("/dev/bus/usb/%0.3d/%0.3d", dev.busID, dev.devID)

		// Envs are a set of environment variables to set for the task.
		resp.Envs[fmt.Sprintf("DEVICE_%d", i)] = id
		resp.Envs[fmt.Sprintf("DEVICE_%d_PATH", i)] = path

		// Devices are the set of devices to mount into the container.
		resp.Devices = append(resp.Devices, &device.DeviceSpec{
			// TaskPath is the location to mount the device in the task's file system.
			TaskPath: path,
			// HostPath is the host location of the device.
			HostPath: path,
			// CgroupPerms defines the permissions to use when mounting the device.
			CgroupPerms: "rx",
		})
	}

	return resp, nil
}
