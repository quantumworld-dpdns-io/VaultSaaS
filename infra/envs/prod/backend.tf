terraform {
  backend "s3" {
    key                         = "prod/terraform.tfstate"
    region                      = "auto"
    use_lockfile                = true # requires R2 conditional writes (Week 0 check); CI also serialises applies
    skip_credentials_validation = true
    skip_region_validation      = true
    skip_requesting_account_id  = true
    skip_metadata_api_check     = true
    skip_s3_checksum            = true
    use_path_style              = true
    # bucket, profile, endpoints: -backend-config=../../backend.r2.hcl.local
  }
}
