package db

//go:generate go tool ent generate --feature sql/upsert,sql/execquery,sql/lock --target ./ent ./schema/nagi
