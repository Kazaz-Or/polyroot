# Payments

The Payments product consists of payments-api, payments-web and
payments-worker.

- shared-sdk contains the shared API contracts; change it first, then its consumers.
- Deployment configuration is in the shared helm repository (`charts/payments`).
- Infrastructure is managed through terraform (`envs/*/payments`).

A user checkout normally flows:

```text
payments-web -> payments-api -> payments-worker
```
