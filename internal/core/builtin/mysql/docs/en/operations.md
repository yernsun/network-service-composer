# Installation and maintenance

MySQL with separate root and application passwords. Connect on port 3306; data: /var/lib/mysql.

Choose a fixed market version, read its parameters, and enter your own secrets. Passwords have no usable default; use at least 16 bytes. Never commit real secrets to the market.

Run preflight, review the Compose definition, volumes and network dependencies, then confirm installation. Inspect the operation and each container health result; running does not by itself mean healthy.

Database services expose internal ports only. Create an HTTP Route with TLS only for web applications, after confirming Gateway access to the service.

Storage and connectivity

Before updating, back up volumes and databases using application-appropriate tools and test restoration. Restoring an image or configuration does not restore business data.

Stopping an application preserves its named volumes. Removing an application must not implicitly remove persistent data. Keep the recovery materials and assign future update responsibility.

There are no published host ports in these templates. External database networks must already exist and be reachable from the application.

Synchronizing the market adds immutable versions; it does not update installed workloads. A withdrawn version remains in history but cannot be installed again.
