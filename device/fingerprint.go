package device

import (
	"context"
	"fmt"
	"time"

	"github.com/google/gousb"
	"github.com/hashicorp/nomad/plugins/device"
	"github.com/pkg/errors"
)

// doFingerprint is the long-running goroutine that detects device changes
func (d *RTL2838DevicePlugin) doFingerprint(ctx context.Context, devices chan *device.FingerprintResponse) {
	defer close(devices)

	// Create a timer that will fire immediately for the first detection
	ticker := time.NewTimer(0)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			ticker.Reset(d.fingerprintPeriod)
		}

		d.writeFingerprintToChannel(devices)
	}
}

type fingerprintedDevice struct {
	ID    string
	name  string
	busID int
	devID int
	vid   uint16
	pid   uint16
}

func newFingerprintedDevice(bus, dev int, vid, pid gousb.ID) *fingerprintedDevice {
	return &fingerprintedDevice{
		ID:    fmt.Sprintf("%d-%d-%d:%d", bus, dev, vid, pid),
		name:  "Realtek Semiconductor Corp. RTL2838 DVB-T",
		busID: bus,
		devID: dev,
		vid:   uint16(vid),
		pid:   uint16(pid),
	}
}

// writeFingerprintToChannel collects fingerprint info, partitions devices into
// device groups, and sends the data over the provided channel.
func (d *RTL2838DevicePlugin) writeFingerprintToChannel(devices chan<- *device.FingerprintResponse) {
	// The logic for fingerprinting devices and detecting the diffs
	// will vary across devices.
	//
	// For this example, we'll create a few virtual devices on the first
	// fingerprinting.
	//
	// Subsequent loops won't do anything, and theoretically, we could just exit
	// this method. However, for non-trivial devices, fingerprinting is an on-going
	// process, useful for detecting new devices and tracking the health of
	// existing devices.
	if len(d.devices) == 0 {
		d.deviceLock.Lock()
		defer d.deviceLock.Unlock()

		discoveredDevices, err := d.discover()
		if err != nil {
			devices <- &device.FingerprintResponse{
				Error: errors.Wrap(err, "error discovering sdr"),
			}
			return
		}

		// during fingerprinting, devices are grouped by "device group" in
		// order to facilitate scheduling
		// devices in the same device group should have the same
		// Vendor, Type, and Name ("Model")
		// Build Fingerprint response with computed groups and send it over the channel
		deviceListByDeviceName := make(map[string][]*fingerprintedDevice)
		for _, device := range discoveredDevices {
			deviceListByDeviceName[device.name] = append(deviceListByDeviceName[device.name], device)
			d.devices[device.ID] = device
		}

		// Build Fingerprint response with computed groups and send it over the channel
		deviceGroups := make([]*device.DeviceGroup, 0, len(deviceListByDeviceName))
		for groupName, devices := range deviceListByDeviceName {
			deviceGroups = append(deviceGroups, deviceGroupFromFingerprintData(groupName, devices))
		}
		devices <- device.NewFingerprint(deviceGroups...)
	}
}

// deviceGroupFromFingerprintData composes deviceGroup from a slice of detected devicers
func deviceGroupFromFingerprintData(groupName string, deviceList []*fingerprintedDevice) *device.DeviceGroup {
	// deviceGroup without devices makes no sense -> return nil when no devices are provided
	if len(deviceList) == 0 {
		return nil
	}

	devices := make([]*device.Device, 0, len(deviceList))
	for _, dev := range deviceList {
		devices = append(devices, &device.Device{
			ID:      dev.ID,
			Healthy: true,
		})
	}

	deviceGroup := &device.DeviceGroup{
		Vendor:  vendor,
		Type:    deviceType,
		Name:    groupName,
		Devices: devices,
		//TODO Figure out what's useful
		Attributes: nil,
	}
	return deviceGroup
}

func (d *RTL2838DevicePlugin) discover() ([]*fingerprintedDevice, error) {
	const (
		// only look for a specific vid/pid for now
		rtl2838vid = 0x0bda
		rtl2838pid = 0x2838
	)

	discovered := []*fingerprintedDevice{}

	// Iterate but do not open devices
	usbctx := gousb.NewContext()

	_, err := usbctx.OpenDevices(func(desc *gousb.DeviceDesc) bool {
		if desc.Vendor != rtl2838vid {
			return false
		}

		if desc.Product != rtl2838pid {
			return false
		}

		discovered = append(discovered, newFingerprintedDevice(
			desc.Bus, desc.Address, desc.Vendor, desc.Product))

		// We're just fingerprinting devices, don't open them
		return false
	})

	if err != nil {
		usbctx.Close()
		return nil, err
	}

	if err := usbctx.Close(); err != nil {
		return nil, err
	}

	return discovered, nil
}
