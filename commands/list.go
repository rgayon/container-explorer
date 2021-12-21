/*
Copyright 2021 Google LLC

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    https://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package commands

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"

	"github.com/containerd/containerd/containers"
	"github.com/containerd/containerd/content"
	"github.com/containerd/containerd/metadata"
	"github.com/containerd/containerd/namespaces"
	"github.com/google/container-explorer/ctrmeta"
	"github.com/opencontainers/go-digest"
	spec "github.com/opencontainers/runtime-spec/specs-go"
	bolt "go.etcd.io/bbolt"

	log "github.com/sirupsen/logrus"
	"github.com/urfave/cli"
)

const (
	tsLayout = "2006-01-02T15:04:05.000000000Z"
)

var knownContainerImage map[string]string

func init() {
	knownContainerImage = make(map[string]string)

	// load GKE supporting container image files
	loadGKEContainerImages()
}

// gkeContainers loads support GKE containers.
func loadGKEContainerImages() {
	knownContainerImage["asia.gcr.io/gke-release-staging/cluster-proportional-autoscaler-amd64"] = "gke"
	knownContainerImage["gcr.io/k8s-ingress-image-push/ingress-gce-404-server-with-metrics"] = "gke"
	knownContainerImage["gke.gcr.io/cluster-proportional-autoscaler"] = "gke"
	knownContainerImage["gke.gcr.io/csi-node-driver-registrar"] = "gke"
	knownContainerImage["gke.gcr.io/event-exporter"] = "gke"
	knownContainerImage["gke.gcr.io/fluent-bit"] = "gke"
	knownContainerImage["gke.gcr.io/fluent-bit-gke-exporter"] = "gke"
	knownContainerImage["gke.gcr.io/gcp-compute-persistent-disk-csi-driver"] = "gke"
	knownContainerImage["gke.gcr.io/gke-metrics-agent"] = "gke"
	knownContainerImage["gke.gcr.io/k8s-dns-dnsmasq-nanny"] = "gke"
	knownContainerImage["gke.gcr.io/k8s-dns-kube-dns"] = "gke"
	knownContainerImage["gke.gcr.io/k8s-dns-sidecar"] = "gke"
	knownContainerImage["gke.gcr.io/kube-proxy-amd64"] = "gke"
	knownContainerImage["gke.gcr.io/prometheus-to-sd"] = "gke"
	knownContainerImage["gke.gcr.io/proxy-agent"] = "gke"
	knownContainerImage["k8s.gcr.io/metrics-server/metrics-server"] = "gke"
	knownContainerImage["k8s.gcr.io/pause"] = "gke"
}

var ListCommand = cli.Command{
	Name:    "list",
	Aliases: []string{"ls"},
	Usage:   "list containerd information",
	Subcommands: cli.Commands{
		listNamespaces,
		listContainers,
		listContent,
		listImages,
		listSnapshots,
		listLeases,
	},
}

var listNamespaces = cli.Command{
	Name:        "namespaces",
	Aliases:     []string{"namespace", "ns"},
	Usage:       "list namespaces",
	Description: "list all namespaces on the system",
	Action: func(cliContext *cli.Context) error {
		ctx, _, db, cancel, err := ctrmeta.GetContainerEnvironment(cliContext)
		if err != nil {
			log.Fatal(err)
		}
		defer cancel()

		nss, err := ctrmeta.GetNamespaces(ctx, db)
		if err != nil {
			log.Fatal(err)
		}

		// handle empty namespaces
		if nss == nil {
			log.Info("namespaces not found in the database")
			return nil
		}

		// print namespaces
		fmt.Println("NAMESPACE")
		for _, ns := range nss {
			fmt.Println(ns)
		}
		return nil
	},
}

// isKnownContainerImage returns true if the image name
// is in knownContainerImage map.
func isKnownContainerImage(image string) bool {
	if strings.Contains(image, "@") {
		imageBase := strings.Split(image, "@")[0]
		k8sType := knownContainerImage[imageBase]

		if k8sType != "" {
			return true
		}
	}

	if strings.Contains(image, ":") {
		imageBase := strings.Split(image, ":")[0]
		k8sType := knownContainerImage[imageBase]

		if k8sType != "" {
			return true
		}
	}

	return false
}

var listContainers = cli.Command{
	Name:        "containers",
	Aliases:     []string{"c"},
	Usage:       "list containers",
	Description: "list all containers",
	Flags: append([]cli.Flag{
		cli.BoolFlag{
			Name:  "skip-known-containers",
			Usage: "Skip known containers",
		},
	}),
	Action: func(clictx *cli.Context) error {

		// open bolt database
		ctx, _, db, cancel, err := ctrmeta.GetContainerEnvironment(clictx)
		if err != nil {
			log.Fatal(err)
		}
		defer cancel()

		store := metadata.NewContainerStore(metadata.NewDB(db, nil, nil))

		// use namespaces from the database
		nss, err := ctrmeta.GetNamespaces(ctx, db)
		if err != nil {
			log.Fatal(err)
		}
		if nss == nil {
			log.Info("namespace bucket does not exist")
		}

		tw := tabwriter.NewWriter(os.Stdout, 1, 8, 1, '\t', 0)
		defer tw.Flush()
		fmt.Fprintf(tw, "\nNAMESPACE\tCONTAINER NAME\tCONTAINER HOSTNAME\tIMAGE\tCREATED AT\tLABELS\n")

		for _, ns := range nss {
			ctx = namespaces.WithNamespace(ctx, ns)
			var filters []string

			results, err := store.List(ctx, filters...)
			if err != nil {
				log.WithField("namespace", ns).Error(err)
				continue
			}

			// handle namespacess without containers
			if results == nil {
				fmt.Fprintf(tw, "%s\t%s\t%s\t%v\t%v\t%s\n",
					ns,
					"", // ID
					"", // containerHostname
					"", // Image
					"", // CreatedAt
					"") // labels

				continue
			}

			// handle namespaces with containers
			for _, result := range results {
				var labelStrings []string
				for k, v := range result.Labels {
					labelStrings = append(labelStrings, strings.Join([]string{k, v}, "="))
				}
				labels := strings.Join(labelStrings, ",")
				if labels == "" {
					labels = "-"
				}

				// Skip the known container images
				if clictx.Bool("skip-known-containers") {
					if isKnownContainerImage(result.Image) {
						continue
					}
				}

				var containerHostname string = ""

				if result.Spec != nil && result.Spec.Value != nil {
					var v spec.Spec
					json.Unmarshal(result.Spec.Value, &v)

					if v.Hostname != "" {
						containerHostname = v.Hostname
					} else {

						for _, kv := range v.Process.Env {
							if strings.HasPrefix(kv, "HOSTNAME=") {
								containerHostname = strings.TrimSpace(strings.Split(kv, "=")[1])
								break
							}
						}
					}
					log.WithFields(log.Fields{
						"containerHostname": v.Hostname,
					}).Debug("Specs data")
				}

				fmt.Fprintf(tw, "%s\t%s\t%s\t%v\t%v\t%s\n",
					ns,
					result.ID,
					containerHostname,
					result.Image,
					result.CreatedAt.Format(tsLayout),
					labels)

			}
		} //__end_of_nss__

		// default return
		return nil
	},
}

var listContent = cli.Command{
	Name:        "content",
	Usage:       "list content",
	Description: "list all containers",
	Action: func(clictx *cli.Context) error {

		ctx, cc, db, cancel, err := ctrmeta.GetContainerEnvironment(clictx)
		if err != nil {
			return err
		}
		defer cancel()

		log.WithFields(log.Fields{
			"root_dir":      cc.RootDir,
			"manifest_file": cc.ManifestFile,
			"snapshot_file": cc.SnapshotFile,
		}).Debug("container config")

		nss, err := ctrmeta.GetNamespaces(ctx, db)
		if err != nil {
			log.Error("error enumerating namespaces. ", err)
			return err
		}

		if nss == nil {
			return fmt.Errorf("no namespace in the bucket")
		}

		tw := tabwriter.NewWriter(os.Stdout, 1, 8, 1, '\t', 0)
		defer tw.Flush()
		fmt.Fprintf(tw, "\nNAMESPACE\tDIGEST\tSIZE\tCREATED AT\tLABELS\n")

		var infos []content.Info
		var infosns []string

		for _, ns := range nss {
			ctx = namespaces.WithNamespace(ctx, ns)

			// Get content information

			if err := db.View(func(tx *bolt.Tx) error {
				contentBucket := ctrmeta.GetBucket(tx,
					ctrmeta.BucketKeyVersion,
					[]byte(ns),
					ctrmeta.BucketKeyObjectContent,
					ctrmeta.BucketKeyObjectBlob)

				if contentBucket == nil {
					log.WithField("namespace", ns).Info("namespace buckeet does not exist")
					infos = append(infos, content.Info{})
					infosns = append(infosns, ns)
					return nil
				}

				if err := contentBucket.ForEach(func(k, v []byte) error {
					// TODO(rmaskey): Determine why digest.Parse(string(k)) generates
					// upsupported algorithm
					// dgst, err1 := digest.Parse(string(k))
					// if err1 != nil {
					//	log.Error(fmt.Sprintf("Error parsing digest %s in namespace %s", string(k), ns))
					//	return err1
					// }

					dgst := digest.Digest(string(k))
					log.WithFields(log.Fields{
						"namespace": ns,
						"digest":    dgst,
					}).Debug("blob digest information")

					info := content.Info{
						Digest: dgst,
					}

					if err := ctrmeta.ReadContentInfo(&info, contentBucket.Bucket(k)); err != nil {
						return err
					}
					infos = append(infos, info)
					infosns = append(infosns, ns)

					return nil
				}); err != nil {
					return err
				}
				return nil
			}); err != nil {
				return fmt.Errorf("error viewing database %v", err)
			}
		}

		// display content
		for i, info := range infos {
			var labelStrings []string
			for k, v := range info.Labels {
				labelStrings = append(labelStrings, strings.Join([]string{k, v}, "="))
			}
			labels := strings.Join(labelStrings, ",")
			if labels == "" {
				labels = "-"
			}

			fmt.Fprintf(tw, "%s\t%s\t%v\t%v\t%s\n",
				infosns[i],
				info.Digest,
				info.Size,
				info.CreatedAt.Format(tsLayout),
				labels)
		}
		// Default action return
		return nil
	},
}

var listImages = cli.Command{
	Name:        "images",
	Aliases:     []string{"image", "img"},
	Usage:       "list images",
	Description: "list all images",
	Action: func(clictx *cli.Context) error {
		ctx, _, db, cancel, err := ctrmeta.GetContainerEnvironment(clictx)
		if err != nil {
			log.Fatal(err)
		}
		defer cancel()

		store := metadata.NewImageStore(metadata.NewDB(db, nil, nil))

		// using namespaces
		nss, err := ctrmeta.GetNamespaces(ctx, db)
		if err != nil {
			return fmt.Errorf("error getting namespaces %v", err)
		}
		if nss == nil {
			return fmt.Errorf("empty namespaces")
		}

		tw := tabwriter.NewWriter(os.Stdout, 1, 8, 1, '\t', 0)
		defer tw.Flush()

		fmt.Fprintf(tw, "NAMESPACE\tNAME\tCREATED AT\tDIGEST\tTYPE\n")

		for _, ns := range nss {
			ctx = namespaces.WithNamespace(ctx, ns)

			var filters []string
			imgs, err := store.List(ctx, filters...)
			if err != nil {
				return err
			}

			// display empty images
			if imgs == nil {
				fmt.Fprintf(tw, "%s\t%s\t%v\t%s\t%s\n", ns, "", "", "", "")
				continue
			}

			// display images
			for _, img := range imgs {
				fmt.Fprintf(tw, "%s\t%s\t%v\t%s\t%s\n",
					ns,
					img.Name,
					img.CreatedAt.Format(tsLayout),
					img.Target.Digest,
					img.Target.MediaType)
			}
		}

		// default return
		return nil
	},
}

var listLeases = cli.Command{
	Name:        "leases",
	Aliases:     []string{"lease"},
	Usage:       "list leases",
	Description: "list leases",
	Action: func(clictx *cli.Context) error {

		ctx, _, db, cancel, err := ctrmeta.GetContainerEnvironment(clictx)
		if err != nil {
			log.Fatal(err)
		}
		defer cancel()

		store := metadata.NewLeaseManager(metadata.NewDB(db, nil, nil))

		// TODO (rmaskey): enumerate namespaces
		//var nss []string
		//nss = []string{"default", "dev", "prod", "non-prod", "test"}

		// use namespaces from the database
		nss, err := ctrmeta.GetNamespaces(ctx, db)
		if err != nil {
			log.Fatal(err)
		}
		if nss == nil {
			log.Printf("Namespaces not found in the database")
		}

		for _, ns := range nss {
			ctx = namespaces.WithNamespace(ctx, ns)
			var filters []string

			results, err := store.List(ctx, filters...)
			if err != nil {
				log.WithField("namespace", ns).Warn("skipping leases information")
				continue
			}

			// handle namespaces without leases
			if results == nil {
				v := make(map[string]interface{})
				v["Namespace"] = ns
				v["Message"] = "No leases for this namespace"

				data, _ := json.MarshalIndent(v, "", " ")
				fmt.Println(string(data))
				continue
			}

			// handle namespaces with leases
			for _, result := range results {
				v := make(map[string]interface{})

				var data []byte
				data, _ = json.Marshal(result)
				json.Unmarshal(data, &v)
				v["Namespace"] = ns
				data, _ = json.MarshalIndent(v, "", " ")
				fmt.Println(string(data))
			}
		}

		return nil
	},
}

var listSnapshots = cli.Command{
	Name:        "snapshots",
	Aliases:     []string{"snapshot"},
	Usage:       "list snapshots",
	Description: "list snapshots",
	Action: func(clictx *cli.Context) error {
		ctx, _, db, cancel, err := ctrmeta.GetContainerEnvironment(clictx)
		if err != nil {
			//log.Fatal(err)
			return fmt.Errorf("error getting container environment %v", err)
		}
		defer cancel()

		nss, err := ctrmeta.GetNamespaces(ctx, db)
		if err != nil {
			log.Error("error listing namespaces ", err)
			return err
		}
		if nss == nil {
			return fmt.Errorf("empty namespace - at least default namespace should exist")
		}

		var infos []ctrmeta.SnapshotKeyInfo
		for _, ns := range nss {
			ctx = namespaces.WithNamespace(ctx, ns)
			nsinfos, err := ctrmeta.ListSnapshots(ctx, db)
			if err != nil {
				log.Error("error listing snapshots for namespace ", ns)
				continue
			}
			infos = append(infos, nsinfos...)
		}
		//
		// Get environmental information about snapshotter database
		//
		// TODO(rmaskey): handle multiple snapshotters
		cinfo := containers.Container{
			Snapshotter: infos[0].Snapshotter,
		}
		ssroot, sdb, cancel, err := ctrmeta.ContainerSnapshotEnvironment(clictx, cinfo)
		if err != nil {
			return fmt.Errorf("error getting snapshot environment %v", err)
		}
		defer cancel()

		log.WithFields(log.Fields{
			"snapshotter_root":     ssroot,
			"snapshotter_database": sdb,
		}).Debug("snapshot metadata database")

		/*
			if ssroot == "" {
				ssroot = "UNKNOWN"
			}
		*/

		if err := sdb.View(func(tx *bolt.Tx) error {
			vbkt := tx.Bucket(ctrmeta.BucketKeyVersion)
			if vbkt == nil {
				return fmt.Errorf("bucket is empty")
			}

			ssbkt := vbkt.Bucket(ctrmeta.BucketKeyObjectSnapshots)
			if ssbkt == nil {
				return fmt.Errorf("snapshots bucket does not exist")
			}

			// prepare output
			tw := tabwriter.NewWriter(os.Stdout, 1, 8, 1, '\t', 0)
			defer tw.Flush()
			fmt.Fprintf(tw, "NAMESPACE\tSNAPSHOTTER\tCREATED AT\tKIND\tNAME\tPARENT\tFSPATH\n")

			for _, info := range infos {
				if info.Key == "" {
					log.WithField("namespace", info.Namespace).Info("no snapshots")
					continue
				}
				sinfo, err := ctrmeta.GetSnapshotInfo(ssbkt, info.Name)
				if err != nil {
					log.WithField("key", info.Name).Error("failed to get snapshot information")
					continue
				}

				sskbkt := ssbkt.Bucket([]byte(info.Name))
				fspath := fmt.Sprintf("%s/snapshots/%d/fs", ssroot, ctrmeta.GetSnapshotID(sskbkt))

				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n",
					info.Namespace,
					info.Snapshotter,
					sinfo.Created.Format(tsLayout),
					sinfo.Kind,
					sinfo.Name,
					sinfo.Parent,
					fspath,
				)
			}
			return nil
		}); err != nil {
			return err
		}

		// default action return
		return nil
	},
}