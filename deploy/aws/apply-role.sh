#!/usr/bin/env bash
# The AWS role dray-server runs as (IRSA): push and read images in ECR, in
# one account and region. Nothing else: no cluster access (that is Kubernetes
# RBAC, deploy/kubernetes/server.yaml) and no secrets (the secret store).
#
#   deploy/aws/apply-role.sh --cluster <eks cluster> --region <region>          dry run
#   deploy/aws/apply-role.sh --cluster <eks cluster> --region <region> --yes    writes to AWS
#
# The account and the cluster's OIDC provider are read live (a rebuilt cluster
# gets a new provider id). The provider must already be registered with IAM.
# Re-running changes nothing that is already right.
set -euo pipefail
CLUSTER="" REGION="" ROLE="dray-server" NAMESPACE="dray" SA="dray-server" YES=""
while [ $# -gt 0 ]; do
  case "$1" in
    --cluster) CLUSTER="$2"; shift 2 ;;
    --region) REGION="$2"; shift 2 ;;
    --role) ROLE="$2"; shift 2 ;;
    --namespace) NAMESPACE="$2"; shift 2 ;;
    --service-account) SA="$2"; shift 2 ;;
    --yes) YES=1; shift ;;
    *) echo "unknown option $1" >&2; exit 2 ;;
  esac
done
[ -n "$CLUSTER" ] && [ -n "$REGION" ] || { sed -n '2,12p' "$0" | sed 's/^# \?//'; exit 2; }

ACCOUNT=$(aws sts get-caller-identity --query Account --output text)
ISSUER=$(aws eks describe-cluster --name "$CLUSTER" --region "$REGION" --query cluster.identity.oidc.issuer --output text)
PROVIDER="${ISSUER#https://}"

trust=$(cat <<JSON
{
  "Version": "2012-10-17",
  "Statement": [{
    "Effect": "Allow",
    "Principal": {"Federated": "arn:aws:iam::$ACCOUNT:oidc-provider/$PROVIDER"},
    "Action": "sts:AssumeRoleWithWebIdentity",
    "Condition": {"StringEquals": {
      "$PROVIDER:sub": "system:serviceaccount:$NAMESPACE:$SA",
      "$PROVIDER:aud": "sts.amazonaws.com"
    }}
  }]
}
JSON
)
policy=$(cat <<JSON
{
  "Version": "2012-10-17",
  "Statement": [
    {"Sid": "Login", "Effect": "Allow", "Action": "ecr:GetAuthorizationToken", "Resource": "*"},
    {"Sid": "ImagesAndBuildCache", "Effect": "Allow", "Action": [
      "ecr:BatchCheckLayerAvailability", "ecr:BatchGetImage", "ecr:GetDownloadUrlForLayer",
      "ecr:InitiateLayerUpload", "ecr:UploadLayerPart", "ecr:CompleteLayerUpload", "ecr:PutImage",
      "ecr:DescribeRepositories", "ecr:DescribeImages", "ecr:CreateRepository"
    ], "Resource": "arn:aws:ecr:$REGION:$ACCOUNT:repository/*"}
  ]
}
JSON
)

echo "Role $ROLE in account $ACCOUNT, assumable only by $NAMESPACE/$SA on $CLUSTER ($REGION)"
echo "It may: log in to ECR; push, read and create repositories in $REGION."
if [ -z "$YES" ]; then
  echo; echo "Trust policy:"; echo "$trust"; echo; echo "Permissions:"; echo "$policy"
  echo; echo "Dry run: nothing written. Pass --yes to apply."
  exit 0
fi
if aws iam get-role --role-name "$ROLE" >/dev/null 2>&1; then
  aws iam update-assume-role-policy --role-name "$ROLE" --policy-document "$trust"
  echo "role exists: trust policy set"
else
  aws iam create-role --role-name "$ROLE" --assume-role-policy-document "$trust" \
    --description "dray-server: push and read images in ECR" >/dev/null
  echo "role created"
fi
aws iam put-role-policy --role-name "$ROLE" --policy-name ecr --policy-document "$policy"
echo "permissions set"
echo "Annotate the service account: eks.amazonaws.com/role-arn=arn:aws:iam::$ACCOUNT:role/$ROLE"
