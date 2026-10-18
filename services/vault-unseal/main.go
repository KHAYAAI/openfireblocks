// Command vault-unseal keeps a Vault replica open.
//
// It runs as a sidecar next to every Vault pod and does two things forever:
// initialise the cluster if nobody has yet, and unseal this replica whenever
// it is found sealed.
//
// Why it exists: Vault with real storage comes back from any restart sealed,
// and a sealed Vault fails every request -- to the rest of the platform it
// is indistinguishable from a Vault that lost its data. Before this, the
// cluster ran `vault server -dev`, which is never sealed because it keeps
// everything in memory and therefore loses it all when the pod moves. Both
// states are outages; this is what makes persistent storage usable without
// a human on call.
//
// The unseal key lives in a Kubernetes Secret rather than on a pod's own
// volume, because each Raft replica has its own PersistentVolumeClaim and a
// key written by one replica is invisible to the others.
//
// NOT PRODUCTION-GRADE KEY HANDLING. A single unseal share, held in a
// Secret in the same cluster it protects, is a deliberate trade for
// unattended recovery on a self-contained deployment. A real deployment
// removes the key from the picture entirely with KMS or HSM auto-unseal
// (a `seal` stanza), at which point this sidecar is not needed at all --
// see docs/security/key-rotation.md.
package main

import (
	"log"
	"os"
	"time"
)

func getenv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func main() {
	vaultAddr := getenv("VAULT_ADDR", "http://127.0.0.1:8200")
	secretName := getenv("UNSEAL_SECRET_NAME", "vault-unseal")
	pollInterval, err := time.ParseDuration(getenv("POLL_INTERVAL", "5s"))
	if err != nil {
		log.Fatalf("invalid POLL_INTERVAL: %v", err)
	}
	podName := getenv("POD_NAME", "vault")

	kube, err := newKubeClient()
	if err != nil {
		log.Fatalf("kubernetes client: %v", err)
	}
	vault := newVaultClient(vaultAddr)

	log.Printf("vault-unseal watching %s for %s (secret: %s/%s)",
		vaultAddr, podName, kube.namespace, secretName)

	// No overall timeout and no exit on error: this is the component that
	// has to keep trying while the thing it depends on is broken. Every
	// failure below is logged and retried on the next tick rather than
	// crash-looping the pod, because a crash-looping sidecar takes its
	// Vault replica's readiness with it.
	for {
		if err := reconcile(vault, kube, secretName, podName); err != nil {
			log.Printf("reconcile: %v", err)
		}
		time.Sleep(pollInterval)
	}
}

// reconcile drives this replica one step closer to unsealed.
func reconcile(vault *vaultClient, kube *kubeClient, secretName, podName string) error {
	status, err := vault.SealStatus()
	if err != nil {
		// Normal during startup: the sidecar and Vault start together and
		// Vault takes a moment to listen.
		return nil
	}

	if !status.Initialized {
		return initialiseCluster(vault, kube, secretName, podName)
	}
	if !status.Sealed {
		return nil
	}

	material, err := kube.GetUnsealMaterial(secretName)
	if err != nil {
		return err
	}
	if material == nil {
		// Initialised but no stored key: either another replica is
		// mid-initialisation, or the Secret was deleted while the cluster
		// lives on. The second case is unrecoverable by this sidecar and
		// worth saying plainly rather than retrying in silence forever.
		log.Printf("%s is initialised but secret %s does not exist; "+
			"if this persists, the unseal key is gone and the cluster cannot be opened", podName, secretName)
		return nil
	}

	after, err := vault.Unseal(material.UnsealKey)
	if err != nil {
		return err
	}
	if after.Sealed {
		return nil
	}
	log.Printf("%s unsealed", podName)
	return nil
}

// initialiseCluster performs the one-time init, or defers to whoever won.
func initialiseCluster(vault *vaultClient, kube *kubeClient, secretName, podName string) error {
	// Check for existing material first. A Secret that already exists means
	// another replica initialised the cluster and this one is looking at a
	// replica that has not yet joined the Raft cluster -- calling init here
	// would be wrong even if Vault let us.
	existing, err := kube.GetUnsealMaterial(secretName)
	if err != nil {
		return err
	}
	if existing != nil {
		log.Printf("%s is uninitialised but the cluster has already been initialised; waiting to join", podName)
		return nil
	}

	material, didInit, err := vault.Init()
	if err != nil {
		return err
	}
	if !didInit {
		// Lost the race between the check above and the call. Fine: the
		// winner is about to store the key.
		return nil
	}

	created, err := kube.CreateUnsealMaterial(secretName, material)
	if err != nil {
		// The cluster is now initialised and this is the only copy of the
		// key. Losing it means losing the cluster, so shout.
		log.Printf("CRITICAL: %s initialised Vault but could not store the unseal key in %s (%v). "+
			"The cluster is initialised and unopenable unless this succeeds on a retry.", podName, secretName, err)
		return err
	}
	if !created {
		log.Printf("%s initialised Vault but another replica had already stored unseal material; "+
			"keeping theirs", podName)
		return nil
	}
	log.Printf("%s initialised the Vault cluster and stored the unseal material in %s", podName, secretName)
	return nil
}
