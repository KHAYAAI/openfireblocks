// vault-unseal is the sidecar that makes a Vault Raft cluster usable
// without a human.
//
// Vault with persistent storage comes back from a restart *sealed*, and a
// sealed Vault is indistinguishable from a destroyed one as far as the rest
// of the platform is concerned: every request fails. Something has to
// initialise the cluster exactly once, keep the unseal material where every
// replica can reach it, and unseal each replica whenever it comes back.
//
// Deliberately no dependencies: it speaks HTTP to Vault and to the
// Kubernetes API directly, the same way services/vault-pki-init does, so
// there is no client-go to keep current in a component whose whole job is
// to be boring and always work.
module forge-crypto/vault-unseal

go 1.24
