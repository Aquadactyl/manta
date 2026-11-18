package config

// applyLegacyDefaults keeps omitted settings in pre-Manta configurations pointing
// at their original data, user and Docker network. Explicit YAML still wins.
// Once saved, the complete configuration records these values with config_version: 1.
func applyLegacyDefaults(c *Configuration) {
	c.System.RootDirectory = "/var/lib/pterodactyl"
	c.System.LogDirectory = "/var/log/pterodactyl"
	c.System.Data = "/var/lib/pterodactyl/volumes"
	c.System.ArchiveDirectory = "/var/lib/pterodactyl/archives"
	c.System.BackupDirectory = "/var/lib/pterodactyl/backups"
	c.System.TmpDirectory = "/tmp/pterodactyl"
	c.System.Username = "pterodactyl"
	c.System.Passwd.Directory = "/run/wings/etc"
	c.System.MachineID.Directory = "/run/wings/machine-id"
	c.Docker.Network.Name = "pterodactyl_nw"
	c.Docker.Network.Mode = "pterodactyl_nw"
	c.Docker.Network.BridgeName = "pterodactyl0"
	// macOS has always used its native per-user directories.
	applyPlatformDefaults(c)
}
