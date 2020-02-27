package device

import (
	"context"
	"time"

	"github.com/hashicorp/nomad/plugins/device"
)

// doStats is the long running goroutine that streams device statistics
func (d *RTL2838DevicePlugin) doStats(ctx context.Context, stats chan<- *device.StatsResponse, interval time.Duration) {
	defer close(stats)

	// Create a timer that will fire immediately for the first detection
	ticker := time.NewTimer(0)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			ticker.Reset(interval)
		}

		d.writeStatsToChannel(stats, time.Now())
	}
}

// deviceStats is what we "collect" and transform into device.DeviceStats objects.
//
// plugin implementations will likely have a native struct provided by the corresonding SDK
type deviceStats struct {
	ID         string
	deviceName string
}

// writeStatsToChannel collects device stats, partitions devices into
// device groups, and sends the data over the provided channel.
func (d *RTL2838DevicePlugin) writeStatsToChannel(stats chan<- *device.StatsResponse, timestamp time.Time) {
	statsData, err := d.collectStats()
	if err != nil {
		d.logger.Error("failed to get device stats", "error", err)
		// Errors should returned in the Error field on the stats channel
		stats <- &device.StatsResponse{
			Error: err,
		}
		return
	}

	// group stats into device groups
	statsListByDeviceName := make(map[string][]*deviceStats)
	for _, statsItem := range statsData {
		deviceName := statsItem.deviceName
		statsListByDeviceName[deviceName] = append(statsListByDeviceName[deviceName], statsItem)
	}

	// create device.DeviceGroupStats struct for every group of stats
	deviceGroupsStats := make([]*device.DeviceGroupStats, 0, len(statsListByDeviceName))
	for groupName, groupStats := range statsListByDeviceName {
		deviceGroupsStats = append(deviceGroupsStats, statsForGroup(groupName, groupStats, timestamp))
	}

	stats <- &device.StatsResponse{
		Groups: deviceGroupsStats,
	}
}

func (d *RTL2838DevicePlugin) collectStats() ([]*deviceStats, error) {
	d.deviceLock.RLock()
	defer d.deviceLock.RUnlock()

	stats := []*deviceStats{}
	for ID, dev := range d.devices {
		stats = append(stats, &deviceStats{
			ID:         ID,
			deviceName: dev.name,
		})
	}

	return stats, nil
}

// statsForGroup is a helper function that populates device.DeviceGroupStats
// for given groupName with groupStats list
func statsForGroup(groupName string, groupStats []*deviceStats, timestamp time.Time) *device.DeviceGroupStats {
	instanceStats := make(map[string]*device.DeviceStats)

	for _, statsItem := range groupStats {
		instanceStats[statsItem.ID] = &device.DeviceStats{
			// Timestamp is the time the statistics were collected.
			Timestamp: timestamp,
		}
	}

	return &device.DeviceGroupStats{
		Vendor:        vendor,
		Type:          deviceType,
		Name:          groupName,
		InstanceStats: instanceStats,
	}
}
