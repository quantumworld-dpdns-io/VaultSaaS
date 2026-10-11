output "core_function_url" {
  description = "Set as CORE_URL in apps/edge/wrangler.toml for this environment."
  value       = module.aws_core.api_function_url
}

output "webhook_queue_url" {
  value = module.aws_core.webhook_queue_url
}

output "function_names" {
  value = module.aws_core.function_names
}

output "edge_iam_user" {
  description = "Mint keys by hand: see docs/runbooks/rotate-edge-key.md"
  value       = module.aws_edge_access.user_name
}

output "r2_buckets" {
  value = module.cloudflare_edge.r2_buckets
}
