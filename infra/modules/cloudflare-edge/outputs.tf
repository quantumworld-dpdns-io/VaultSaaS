output "r2_buckets" {
  value = { for k, b in cloudflare_r2_bucket.this : k => b.name }
}

output "pages_project" {
  value = cloudflare_pages_project.web.name
}
