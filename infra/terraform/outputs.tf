output "api_hostname" {
  value = aws_lb.main.dns_name
}
output "api_repository" {
  value = aws_ecr_repository.api.repository_url
}
output "worker_repository" {
  value = aws_ecr_repository.worker.repository_url
}
output "cluster_name" {
  value = aws_ecs_cluster.main.name
}
