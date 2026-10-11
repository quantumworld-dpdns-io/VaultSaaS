package main

import rego.v1

rc(type, after) := {"address": sprintf("%s.x", [type]), "type": type, "mode": "managed", "change": {"actions": ["create"], "after": after}}

ok_lambda := {"architectures": ["arm64"], "memory_size": 128, "timeout": 10, "vpc_config": [], "tracing_config": [{"mode": "PassThrough"}]}

test_allowed_stack_passes if {
	count(deny) == 0 with input as {"resource_changes": [
		rc("aws_lambda_function", ok_lambda),
		rc("aws_sqs_queue", {}),
		rc("aws_ssm_parameter", {"tier": "Standard", "key_id": "alias/aws/ssm"}),
		rc("aws_cloudwatch_log_group", {"retention_in_days": 7}),
		rc("aws_lambda_event_source_mapping", {"scaling_config": [{"maximum_concurrency": 2}]}),
		rc("cloudflare_r2_bucket", {}),
	]}
}

test_s3_is_denied if {
	some msg in deny with input as {"resource_changes": [rc("aws_s3_bucket", {})]}
	contains(msg, "R2")
}

test_api_gateway_is_denied if {
	count(deny) == 1 with input as {"resource_changes": [rc("aws_apigatewayv2_api", {})]}
}

test_unknown_aws_type_is_denied if {
	some msg in deny with input as {"resource_changes": [rc("aws_dynamodb_table", {})]}
	contains(msg, "allowlist")
}

test_access_key_is_denied if {
	count(deny) == 1 with input as {"resource_changes": [rc("aws_iam_access_key", {})]}
}

test_worker_secret_in_terraform_is_denied if {
	count(deny) == 1 with input as {"resource_changes": [rc("cloudflare_workers_secret", {})]}
}

test_advanced_ssm_is_denied if {
	count(deny) == 1 with input as {"resource_changes": [rc("aws_ssm_parameter", {"tier": "Advanced"})]}
}

test_cmk_ssm_is_denied if {
	count(deny) == 1 with input as {"resource_changes": [rc("aws_ssm_parameter", {"tier": "Standard", "key_id": "arn:aws:kms:x:1:key/abc"})]}
}

test_log_retention_required if {
	count(deny) == 1 with input as {"resource_changes": [rc("aws_cloudwatch_log_group", {"retention_in_days": 0})]}
	count(deny) == 1 with input as {"resource_changes": [rc("aws_cloudwatch_log_group", {"retention_in_days": 30})]}
}

test_lambda_rules if {
	count(deny) == 1 with input as {"resource_changes": [rc("aws_lambda_function", object.union(ok_lambda, {"architectures": ["x86_64"]}))]}
	count(deny) == 1 with input as {"resource_changes": [rc("aws_lambda_function", object.union(ok_lambda, {"memory_size": 1024}))]}
	count(deny) == 1 with input as {"resource_changes": [rc("aws_lambda_function", object.union(ok_lambda, {"timeout": 900}))]}
	count(deny) == 1 with input as {"resource_changes": [rc("aws_lambda_function", object.union(ok_lambda, {"vpc_config": [{"subnet_ids": ["s"]}]}))]}
	count(deny) == 1 with input as {"resource_changes": [rc("aws_lambda_function", object.union(ok_lambda, {"tracing_config": [{"mode": "Active"}]}))]}
}

test_uncapped_event_source_is_denied if {
	count(deny) == 1 with input as {"resource_changes": [rc("aws_lambda_event_source_mapping", {"scaling_config": []})]}
}

test_too_many_alarms if {
	alarms := [a |
		some i in numbers.range(1, 6)
		a := object.union(rc("aws_cloudwatch_metric_alarm", {}), {"address": sprintf("alarm.%d", [i])})
	]
	count(deny) == 1 with input as {"resource_changes": alarms}
}

test_deletes_are_ignored if {
	del := {"address": "aws_s3_bucket.old", "type": "aws_s3_bucket", "mode": "managed", "change": {"actions": ["delete"], "after": null}}
	count(deny) == 0 with input as {"resource_changes": [del]}
}
