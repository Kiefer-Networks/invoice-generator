# Provisioning server document storage

Before starting `cmd/server`, provision a persistent, protected directory and set
`INVOICE_DOCUMENT_ROOT` or `-document-root` to its absolute path. The directory and
every ancestor must already exist and be durably persisted by deployment
provisioning. Server initialization never creates them. A missing component stops
startup before recovery, workers, or document publication; it does not leave
partially created directories. Retry startup after provisioning completes.

The directory must be owned by the server identity, with no access for unrelated
accounts (mode `0700` on POSIX, a restricted directory/volume ACL on Windows).
Configure a direct path: symlinks, junctions, traversal components, and files in
the ancestor chain are rejected. The server opens each existing component through
directory handles and checks its identity before proceeding. Protect ancestors
against rename or replacement by untrusted accounts as part of deployment.

On POSIX, provisioning must persist each newly created directory entry by syncing
its containing directory, including any new ancestors, before starting the server.
A successful `mkdir -p` alone is not a durability guarantee. Use a provisioner that
performs these barriers, or a persistent directory already durably provisioned by
the host/volume setup. Runtime artifact publication separately syncs each file,
renames it, and syncs its containing storage directory before committing ready
metadata in SQLite.

On Windows, the same pre-provisioned-directory contract applies. Windows has no
POSIX directory-fsync equivalent, so this application does not create directories
and treat a file flush as proof that new ancestors are durable. Deployment must
complete its filesystem/volume provisioning and persistence guarantees before
starting the server. Runtime publication uses the documented post-rename
`FlushFileBuffers` file-metadata barrier; unsupported flush errors stop publication.

Development mode may choose the default `documents` directory beside its SQLite
database, but that directory must also be provisioned before startup. Production
requires an explicit absolute path. The existing-root check establishes this
configuration precondition; it cannot retrospectively prove that an external
provisioner issued the required persistence barriers.

Initialization failures return no usable document service or worker controls and
leave queued jobs unclaimed. The server does not delete existing directories or
their contents on startup failure. Root/ancestor provisioning belongs in deployment
setup, including the container volume setup, before the server process runs.
