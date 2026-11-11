variable "region" {
  type    = string
  default = "us-east-1"
}
variable "frontend_origin" {
  type        = string
  description = "Exact HTTPS frontend origin allowed by CORS"
}
variable "certificate_arn" {
  type        = string
  description = "Validated ACM certificate in the selected region"
}
variable "api_keys_secret_arn" {
  type        = string
  description = "Secrets Manager ARN containing a JSON map of API keys to tenant UUIDs"
}
variable "image_tag" {
  type    = string
  default = "v1.0.0"
}
variable "desired_count" {
  type        = number
  default     = 0
  description = "Keep zero until images are pushed and tenant records provisioned"
}
variable "postgres_version" {
  type    = string
  default = "16"
}
variable "kafka_version" {
  type    = string
  default = "3.8.x"
}
variable "allowed_client_cidrs" {
  type        = list(string)
  description = "Explicit IPv4 CIDRs allowed to reach HTTPS; use your own IP during evaluation"
}
