/*
 * MinIO Cloud Storage, (C) 2019 MinIO, Inc.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package cmd

import (
	"net/http"
	"os"

	"github.com/minio/minio-go/pkg/set"
	"github.com/minio/minio/pkg/cpu"
	"github.com/minio/minio/pkg/disk"
	"github.com/minio/minio/pkg/madmin"
	"github.com/minio/minio/pkg/mem"

	cpuhw "github.com/shirou/gopsutil/cpu"
)

// getLocalMemUsage - returns ServerMemUsageInfo for only the
// local endpoints from given list of endpoints
func getLocalMemUsage(endpoints EndpointList, r *http.Request) ServerMemUsageInfo {
	var memUsages []mem.Usage
	var historicUsages []mem.Usage
	seenHosts := set.NewStringSet()
	for _, endpoint := range endpoints {
		if seenHosts.Contains(endpoint.Host) {
			continue
		}
		seenHosts.Add(endpoint.Host)

		// Only proceed for local endpoints
		if endpoint.IsLocal {
			memUsages = append(memUsages, mem.GetUsage())
			historicUsages = append(historicUsages, mem.GetHistoricUsage())
		}
	}
	addr := r.Host
	if globalIsDistXL {
		addr = GetLocalPeer(endpoints)
	}
	return ServerMemUsageInfo{
		Addr:          addr,
		Usage:         memUsages,
		HistoricUsage: historicUsages,
	}
}

// getLocalCPULoad - returns ServerCPULoadInfo for only the
// local endpoints from given list of endpoints
func getLocalCPULoad(endpoints EndpointList, r *http.Request) ServerCPULoadInfo {
	var cpuLoads []cpu.Load
	var historicLoads []cpu.Load
	seenHosts := set.NewStringSet()
	for _, endpoint := range endpoints {
		if seenHosts.Contains(endpoint.Host) {
			continue
		}
		seenHosts.Add(endpoint.Host)

		// Only proceed for local endpoints
		if endpoint.IsLocal {
			cpuLoads = append(cpuLoads, cpu.GetLoad())
			historicLoads = append(historicLoads, cpu.GetHistoricLoad())
		}
	}
	addr := r.Host
	if globalIsDistXL {
		addr = GetLocalPeer(endpoints)
	}
	return ServerCPULoadInfo{
		Addr:         addr,
		Load:         cpuLoads,
		HistoricLoad: historicLoads,
	}
}

// getLocalDrivesPerf - returns ServerDrivesPerfInfo for only the
// local endpoints from given list of endpoints
func getLocalDrivesPerf(endpoints EndpointList, size int64, r *http.Request) madmin.ServerDrivesPerfInfo {
	var dps []disk.Performance
	for _, endpoint := range endpoints {
		// Only proceed for local endpoints
		if endpoint.IsLocal {
			if _, err := os.Stat(endpoint.Path); err != nil {
				// Since this drive is not available, add relevant details and proceed
				dps = append(dps, disk.Performance{Path: endpoint.Path, Error: err.Error()})
				continue
			}
			dp := disk.GetPerformance(pathJoin(endpoint.Path, minioMetaTmpBucket, mustGetUUID()), size)
			dp.Path = endpoint.Path
			dps = append(dps, dp)
		}
	}
	addr := r.Host
	if globalIsDistXL {
		addr = GetLocalPeer(endpoints)
	}
	return madmin.ServerDrivesPerfInfo{
		Addr: addr,
		Perf: dps,
		Size: size,
	}
}

// getLocalCPUInfo - returns ServerCPUHardwareInfo only for the
// local endpoints from given list of endpoints
func getLocalCPUInfo(endpoints EndpointList, r *http.Request) madmin.ServerCPUHardwareInfo {
	var cpuHardwares []cpuhw.InfoStat
	seenHosts := set.NewStringSet()
	for _, endpoint := range endpoints {
		if seenHosts.Contains(endpoint.Host) {
			continue
		}
		// Only proceed for local endpoints
		if endpoint.IsLocal {
			cpuHardware, err := cpuhw.Info()
			if err != nil {
				return madmin.ServerCPUHardwareInfo{
					Error: err.Error(),
				}
			}
			cpuHardwares = append(cpuHardwares, cpuHardware...)
		}
	}
	addr := r.Host
	if globalIsDistXL {
		addr = GetLocalPeer(endpoints)
	}

	return madmin.ServerCPUHardwareInfo{
		Addr:    addr,
		CPUInfo: cpuHardwares,
	}
}
