output "function_names" {
  value = { for k, f in aws_lambda_function.fn : k => f.function_name }
}

output "api_function_arn" {
  value = aws_lambda_function.fn["api"].arn
}

output "api_function_url" {
  value = aws_lambda_function_url.api.function_url
}

output "webhook_queue_arn" {
  value = aws_sqs_queue.webhook.arn
}

output "webhook_queue_url" {
  value = aws_sqs_queue.webhook.url
}

output "alerts_topic_arn" {
  value = aws_sns_topic.alerts.arn
}
