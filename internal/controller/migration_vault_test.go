package controllers

import (
	"testing"
)

func TestMigrationPreservesVaultBackend(t *testing.T) {
	// v1.1 ConfigMap with Vault backend
	oldConfig := `[http_server]
sockets = ["0.0.0.0:8080"]
insecure_http = true

[admin]
type = "DenyAll"
insecure_api = true

[attestation_token]
insecure_key = true

[attestation_service]
type = "coco_as_grpc"
as_addr = "http://127.0.0.1:50004"

# USER CUSTOMIZATION: Vault backend
[[plugins]]
name = "resource"
type = "Vault"
vault_address = "https://vault.example.com:8200"
vault_token = "s.mytoken123"
vault_path = "secret/data/kbs"

[policy_engine]
policy_path = "/opt/confidential-containers/opa/policy.rego"

[attestation_service.rvps_config.storage]
type = "LocalJson"
file_path = "/opt/confidential-containers/rvps/reference-values/reference-values.json"
`

	// Apply migrations
	migrated := oldConfig

	// Storage directory consolidation
	migrated = replaceString(migrated,
		`dir_path = "/opt/confidential-containers/kbs/repository"`,
		`dir_path = "/opt/confidential-containers/storage/repository"`)

	// Policy path migration
	migrated = replaceString(migrated,
		`policy_path = "/opt/confidential-containers/opa/policy.rego"`,
		`policy_path = "/opt/confidential-containers/storage/kbs/resource-policy.rego"`)

	// RVPS storage type field rename
	migrated = replaceString(migrated,
		`type = "LocalJson"`,
		`storage_type = "LocalJson"`)

	// Migrate [admin] section
	migrated = replaceString(migrated,
		`type = "DenyAll"`,
		`authorization_mode = "DenyAll"`)

	// Migrate [attestation_token] section
	migrated = replaceString(migrated,
		`insecure_key = true`,
		`insecure_header_jwk = true`)

	// RVPS file_path migration
	if containsString(migrated, `file_path = "/opt/confidential-containers/rvps/reference-values`) {
		migrated = migrateRvpsStorageSection(migrated)
	}

	// Verify Vault backend is preserved
	if !containsString(migrated, `type = "Vault"`) {
		t.Error("Migration lost Vault backend type")
	}
	if !containsString(migrated, `vault_address = "https://vault.example.com:8200"`) {
		t.Error("Migration lost vault_address")
	}
	if !containsString(migrated, `vault_token = "s.mytoken123"`) {
		t.Error("Migration lost vault_token")
	}
	if !containsString(migrated, `vault_path = "secret/data/kbs"`) {
		t.Error("Migration lost vault_path")
	}

	// Verify v1.1 -> v1.2 transformations happened
	if !containsString(migrated, `authorization_mode = "DenyAll"`) {
		t.Error("Migration didn't update admin.type to admin.authorization_mode")
	}
	if !containsString(migrated, `insecure_header_jwk = true`) {
		t.Error("Migration didn't update attestation_token.insecure_key")
	}
	if !containsString(migrated, `policy_path = "/opt/confidential-containers/storage/kbs/resource-policy.rego"`) {
		t.Error("Migration didn't update policy_path")
	}
	if !containsString(migrated, `storage_type = "LocalJson"`) {
		t.Error("Migration didn't rename type to storage_type")
	}

	// Verify old fields are gone
	if containsString(migrated, `type = "DenyAll"`) {
		t.Error("Migration didn't remove old admin.type field")
	}
	if containsString(migrated, `insecure_key = true`) {
		t.Error("Migration didn't remove old insecure_key field")
	}

	t.Logf("Migrated config:\n%s", migrated)
}
