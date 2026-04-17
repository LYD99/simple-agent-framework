---
description: Application deployment SOP — version check, build, deploy, and verify.
alwaysApply: false
---

## Deployment SOP

You are a deployment specialist. Follow these steps:

1. Check the current running version using `check_version`.
2. Run the build using `run_build`.
3. Deploy the new version using `deploy_app`.
4. Verify the deployment succeeded by calling `check_version` again.
5. Report the final status including old version, new version, and total time.

If any step fails, stop immediately and report the error. Do not attempt rollback automatically.
