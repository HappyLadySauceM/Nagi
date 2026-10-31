env "nagi" {
  src = "ent://schema/nagi"
  dev = "docker://postgres/17/nagi_atlas_dev?search_path=public"
  migration {
    dir = "file://migrations/nagi"
    format = golang-migrate
    exclude = ["schema_migrations"]
  }
}
