package main

import (
	"flag"
	"fmt"
	"log"
	"time"

	"github.com/anilyagiz/automdm/v4/server/automdm"
	"github.com/anilyagiz/automdm/v4/server/service"
)

func printf(format string, a ...any) {
	fmt.Printf(time.Now().UTC().Format("2006-01-02T15:04:05Z")+": "+format, a...)
}

func batchHostnames(hostnames []string) [][]string {
	const batchSize = 500
	batches := make([][]string, 0, (len(hostnames)+batchSize-1)/batchSize)

	for batchSize < len(hostnames) {
		hostnames, batches = hostnames[batchSize:], append(batches, hostnames[0:batchSize:batchSize])
	}
	batches = append(batches, hostnames)
	return batches
}

func main() {
	automdmURL := flag.String("automdm_url", "", "URL (with protocol and port of AutoMDM server)")
	apiToken := flag.String("api_token", "", "API authentication token to use on API calls")
	debug := flag.Bool("debug", false, "Debug mode")

	flag.Parse()

	if *automdmURL == "" {
		log.Fatal("missing automdm_url argument")
	}
	if *apiToken == "" {
		log.Fatal("missing api_token argument")
	}
	var clientOpts []service.ClientOption
	if *debug {
		clientOpts = append(clientOpts, service.EnableClientDebug())
	}
	apiClient, err := service.NewClient(*automdmURL, true, "", "", clientOpts...)
	if err != nil {
		panic(err)
	}
	apiClient.SetToken(*apiToken)

	printf("Fetching hosts...\n")
	records, err := apiClient.GetHostsReport("hostname", "platform")
	if err != nil {
		panic(err)
	}
	var (
		macOSHosts   []string
		windowsHosts []string
		linuxHosts   []string
	)
	for i, record := range records {
		if i == 0 {
			continue
		}
		hostname := record[0]
		platform := automdm.PlatformFromHost(record[1])
		switch platform {
		case "linux":
			linuxHosts = append(linuxHosts, hostname)
		case "darwin":
			macOSHosts = append(macOSHosts, hostname)
		case "windows":
			windowsHosts = append(windowsHosts, hostname)
		}
	}
	printf("Got linux=%d, windows=%d, macOS=%d\n", len(linuxHosts), len(windowsHosts), len(macOSHosts))

	printf("Applying manual labels...\n")
	for _, labelSpec := range []*automdm.LabelSpec{
		// Applying a static/manual label to only 80% of linux hosts.
		{
			Name:                "Manual Label For Linux Hosts",
			LabelMembershipType: automdm.LabelMembershipTypeManual,
			Hosts:               linuxHosts[:int(0.8*float64(len(linuxHosts)))],
		},
		// Apply 4 static/manual labels to all macOS hosts.
		// This is to add more entries to the `labels` and `label_membership` tables.
		{
			Name:                "Manual Label macOS 1",
			LabelMembershipType: automdm.LabelMembershipTypeManual,
			Hosts:               macOSHosts,
		},
		{
			Name:                "Manual Label macOS 2",
			LabelMembershipType: automdm.LabelMembershipTypeManual,
			Hosts:               macOSHosts,
		},
		{
			Name:                "Manual Label macOS 3",
			LabelMembershipType: automdm.LabelMembershipTypeManual,
			Hosts:               macOSHosts,
		},
		{
			Name:                "Manual Label macOS 4",
			LabelMembershipType: automdm.LabelMembershipTypeManual,
			Hosts:               macOSHosts,
		},
		// Apply 5 static/manual labels to all Windows hosts.
		// This is to add more entries to the `labels` and `label_membership` tables.
		{
			Name:                "Manual Label Windows 1",
			LabelMembershipType: automdm.LabelMembershipTypeManual,
			Hosts:               windowsHosts,
		},
		{
			Name:                "Manual Label Windows 2",
			LabelMembershipType: automdm.LabelMembershipTypeManual,
			Hosts:               windowsHosts,
		},
		{
			Name:                "Manual Label Windows 3",
			LabelMembershipType: automdm.LabelMembershipTypeManual,
			Hosts:               windowsHosts,
		},
		{
			Name:                "Manual Label Windows 4",
			LabelMembershipType: automdm.LabelMembershipTypeManual,
			Hosts:               windowsHosts,
		},
		{
			Name:                "Manual Label Windows 5",
			LabelMembershipType: automdm.LabelMembershipTypeManual,
			Hosts:               windowsHosts,
		},
	} {
		for _, batch := range batchHostnames(labelSpec.Hosts) {
			labelSpecSubset := *labelSpec
			labelSpecSubset.Hosts = batch
			printf("Applying label %s to %d hosts...\n", labelSpecSubset.Name, len(labelSpecSubset.Hosts))
			if err := apiClient.ApplyLabels([]*automdm.LabelSpec{&labelSpecSubset}, nil, nil); err != nil {
				panic(err)
			}
			printf("Applied label %s to %d hosts\n", labelSpecSubset.Name, len(labelSpecSubset.Hosts))
		}
		printf("Applied %s\n", labelSpec.Name)
	}
}
