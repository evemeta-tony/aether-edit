-- services/tenancy/migrations/000001_init.down.sql


DROP TABLE IF EXISTS tenancy_usage_rollups;
DROP TABLE IF EXISTS tenancy_metering_events;
DROP TABLE IF EXISTS tenancy_api_keys;
DROP TABLE IF EXISTS tenancy_login_states;
DROP TABLE IF EXISTS tenancy_refresh_tokens;
DROP TABLE IF EXISTS tenancy_memberships;
DROP TABLE IF EXISTS tenancy_workspaces;
DROP TABLE IF EXISTS tenancy_users;

