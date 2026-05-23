*** Settings ***
Documentation       Live reconciliation edge contracts for manual drift and external mounted object changes.
Resource            resources/local.resource
Suite Setup         Ensure Local XTrinode Ready
Suite Teardown      Cleanup Reconciliation Edge Contracts
Test Tags           local    k3d    contracts    reconciliation
Test Teardown       Run Keyword If Test Failed    Dump Debug

*** Variables ***
${MOUNTED_CONFIG}       xtrinode-e2e-mounted-config
${MOUNTED_SECRET}       xtrinode-e2e-mounted-secret

*** Test Cases ***
Manual Coordinator Deployment Scale Drift Is Reconciled
    Command Should Succeed    kubectl    scale    deployment/trino-${XTRINODE_NAME}-coordinator    -n    ${NAMESPACE}    --replicas=0
    Wait Until Keyword Succeeds    180s    ${POLL_INTERVAL}    Deployment Spec Replicas Should Equal    ${NAMESPACE}    trino-${XTRINODE_NAME}-coordinator    1
    Wait Until Keyword Succeeds    ${WAIT_TIMEOUT}    ${POLL_INTERVAL}    Deployment Should Be Available    ${NAMESPACE}    trino-${XTRINODE_NAME}-coordinator    1

Manual Gateway Route ConfigMap Drift Is Reconciled
    ${patch}=    Set Variable    {"data":{"routes.yaml":"routes: []\\n"}}
    Command Should Succeed    kubectl    patch    configmap/trino-gateway-routes    -n    ${GATEWAY_NAMESPACE}    --type=merge    -p    ${patch}
    Wait Until Keyword Succeeds    180s    ${POLL_INTERVAL}    Gateway Route Should Be Registered

Routing Metadata Change Does Not Roll Runtime Pods
    ${coordinator_hash}=    Kubectl Output    get    deployment    trino-${XTRINODE_NAME}-coordinator    -n    ${NAMESPACE}    -o    ${COORDINATOR_ROLLOUT_HASH_OUTPUT}
    ${worker_hash}=    Kubectl Output    get    deployment    trino-${XTRINODE_NAME}-worker    -n    ${NAMESPACE}    -o    ${WORKER_ROLLOUT_HASH_OUTPUT}
    ${coordinator_revision}=    Kubectl Output    get    deployment    trino-${XTRINODE_NAME}-coordinator    -n    ${NAMESPACE}    -o    ${COORDINATOR_TEMPLATE_REVISION_OUTPUT}
    ${worker_revision}=    Kubectl Output    get    deployment    trino-${XTRINODE_NAME}-worker    -n    ${NAMESPACE}    -o    ${WORKER_TEMPLATE_REVISION_OUTPUT}
    ${coordinator_deployment_revision}=    Kubectl Output    get    deployment    trino-${XTRINODE_NAME}-coordinator    -n    ${NAMESPACE}    -o    ${COORDINATOR_DEPLOYMENT_REVISION_OUTPUT}
    ${worker_deployment_revision}=    Kubectl Output    get    deployment    trino-${XTRINODE_NAME}-worker    -n    ${NAMESPACE}    -o    ${WORKER_DEPLOYMENT_REVISION_OUTPUT}
    ${coordinator_config}=    Kubectl Output    get    deployment    trino-${XTRINODE_NAME}-coordinator    -n    ${NAMESPACE}    -o    ${COORDINATOR_CONFIG_CHECKSUM_OUTPUT}
    ${worker_config}=    Kubectl Output    get    deployment    trino-${XTRINODE_NAME}-worker    -n    ${NAMESPACE}    -o    ${WORKER_CONFIG_CHECKSUM_OUTPUT}

    ${routing_group}=    Set Variable    ${XTRINODE_NAME}-shared
    Command Should Succeed    kubectl    annotate    xtrinode/${XTRINODE_NAME}    -n    ${NAMESPACE}    xtrinode.analytics.xtrinode.io/allow-breaking-spec-update=true    --overwrite
    ${patch}=    Set Variable    {"spec":{"routing":{"routingGroup":"${routing_group}"}}}
    Command Should Succeed    kubectl    patch    xtrinode/${XTRINODE_NAME}    -n    ${NAMESPACE}    --type=merge    -p    ${patch}
    Wait Until Keyword Succeeds    180s    ${POLL_INTERVAL}    Gateway Route Should Have Routing Group    ${routing_group}

    Deployment Pod Template Annotation Should Equal    ${NAMESPACE}    trino-${XTRINODE_NAME}-coordinator    ${COORDINATOR_ROLLOUT_HASH_OUTPUT}    ${coordinator_hash}
    Deployment Pod Template Annotation Should Equal    ${NAMESPACE}    trino-${XTRINODE_NAME}-worker    ${WORKER_ROLLOUT_HASH_OUTPUT}    ${worker_hash}
    Deployment Pod Template Annotation Should Equal    ${NAMESPACE}    trino-${XTRINODE_NAME}-coordinator    ${COORDINATOR_TEMPLATE_REVISION_OUTPUT}    ${coordinator_revision}
    Deployment Pod Template Annotation Should Equal    ${NAMESPACE}    trino-${XTRINODE_NAME}-worker    ${WORKER_TEMPLATE_REVISION_OUTPUT}    ${worker_revision}
    Deployment Annotation Should Equal    ${NAMESPACE}    trino-${XTRINODE_NAME}-coordinator    ${COORDINATOR_DEPLOYMENT_REVISION_OUTPUT}    ${coordinator_deployment_revision}
    Deployment Annotation Should Equal    ${NAMESPACE}    trino-${XTRINODE_NAME}-worker    ${WORKER_DEPLOYMENT_REVISION_OUTPUT}    ${worker_deployment_revision}
    Deployment Pod Template Annotation Should Equal    ${NAMESPACE}    trino-${XTRINODE_NAME}-coordinator    ${COORDINATOR_CONFIG_CHECKSUM_OUTPUT}    ${coordinator_config}
    Deployment Pod Template Annotation Should Equal    ${NAMESPACE}    trino-${XTRINODE_NAME}-worker    ${WORKER_CONFIG_CHECKSUM_OUTPUT}    ${worker_config}

External Mounted ConfigMap And Secret Changes Roll Runtime
    Ensure External Mounted Resources
    Patch XTrinode With External Mounts
    Command Should Succeed    kubectl    rollout    status    deployment/trino-${XTRINODE_NAME}-coordinator    -n    ${NAMESPACE}    --timeout=300s
    Command Should Succeed    kubectl    rollout    status    deployment/trino-${XTRINODE_NAME}-worker    -n    ${NAMESPACE}    --timeout=300s

    ${coordinator_before_config}=    Kubectl Output    get    deployment    trino-${XTRINODE_NAME}-coordinator    -n    ${NAMESPACE}    -o    ${COORDINATOR_ROLLOUT_HASH_OUTPUT}
    ${worker_before_config}=    Kubectl Output    get    deployment    trino-${XTRINODE_NAME}-worker    -n    ${NAMESPACE}    -o    ${WORKER_ROLLOUT_HASH_OUTPUT}
    ${config_patch}=    Set Variable    {"data":{"settings.properties":"version=2"}}
    Command Should Succeed    kubectl    patch    configmap/${MOUNTED_CONFIG}    -n    ${NAMESPACE}    --type=merge    -p    ${config_patch}
    Wait Until Keyword Succeeds    180s    ${POLL_INTERVAL}    Deployment Pod Template Annotation Should Not Equal    ${NAMESPACE}    trino-${XTRINODE_NAME}-coordinator    ${COORDINATOR_ROLLOUT_HASH_OUTPUT}    ${coordinator_before_config}
    Wait Until Keyword Succeeds    180s    ${POLL_INTERVAL}    Deployment Pod Template Annotation Should Not Equal    ${NAMESPACE}    trino-${XTRINODE_NAME}-worker    ${WORKER_ROLLOUT_HASH_OUTPUT}    ${worker_before_config}
    Command Should Succeed    kubectl    rollout    status    deployment/trino-${XTRINODE_NAME}-coordinator    -n    ${NAMESPACE}    --timeout=300s
    Command Should Succeed    kubectl    rollout    status    deployment/trino-${XTRINODE_NAME}-worker    -n    ${NAMESPACE}    --timeout=300s

    ${coordinator_before_secret}=    Kubectl Output    get    deployment    trino-${XTRINODE_NAME}-coordinator    -n    ${NAMESPACE}    -o    ${COORDINATOR_ROLLOUT_HASH_OUTPUT}
    ${worker_before_secret}=    Kubectl Output    get    deployment    trino-${XTRINODE_NAME}-worker    -n    ${NAMESPACE}    -o    ${WORKER_ROLLOUT_HASH_OUTPUT}
    ${secret_patch}=    Set Variable    {"data":{"token":"c2Vjb25k"}}
    Command Should Succeed    kubectl    patch    secret/${MOUNTED_SECRET}    -n    ${NAMESPACE}    --type=merge    -p    ${secret_patch}
    Wait Until Keyword Succeeds    180s    ${POLL_INTERVAL}    Deployment Pod Template Annotation Should Not Equal    ${NAMESPACE}    trino-${XTRINODE_NAME}-coordinator    ${COORDINATOR_ROLLOUT_HASH_OUTPUT}    ${coordinator_before_secret}
    Wait Until Keyword Succeeds    180s    ${POLL_INTERVAL}    Deployment Pod Template Annotation Should Not Equal    ${NAMESPACE}    trino-${XTRINODE_NAME}-worker    ${WORKER_ROLLOUT_HASH_OUTPUT}    ${worker_before_secret}
    Command Should Succeed    kubectl    rollout    status    deployment/trino-${XTRINODE_NAME}-coordinator    -n    ${NAMESPACE}    --timeout=300s
    Command Should Succeed    kubectl    rollout    status    deployment/trino-${XTRINODE_NAME}-worker    -n    ${NAMESPACE}    --timeout=300s

External Env ValueFrom And EnvFrom Changes Roll Runtime
    Ensure External Mounted Resources
    Patch XTrinode With External Env Refs
    Wait Until Keyword Succeeds    180s    ${POLL_INTERVAL}    Deployment Should Have External Env Refs In Cluster    ${NAMESPACE}    trino-${XTRINODE_NAME}-coordinator
    Wait Until Keyword Succeeds    180s    ${POLL_INTERVAL}    Deployment Should Have External Env Refs In Cluster    ${NAMESPACE}    trino-${XTRINODE_NAME}-worker

    ${coordinator_before_config}=    Kubectl Output    get    deployment    trino-${XTRINODE_NAME}-coordinator    -n    ${NAMESPACE}    -o    ${COORDINATOR_ROLLOUT_HASH_OUTPUT}
    ${worker_before_config}=    Kubectl Output    get    deployment    trino-${XTRINODE_NAME}-worker    -n    ${NAMESPACE}    -o    ${WORKER_ROLLOUT_HASH_OUTPUT}
    ${config_patch}=    Set Variable    {"data":{"settings.properties":"version=env-config","E2E_ENV_FROM_CONFIG":"second"}}
    Command Should Succeed    kubectl    patch    configmap/${MOUNTED_CONFIG}    -n    ${NAMESPACE}    --type=merge    -p    ${config_patch}
    Wait Until Keyword Succeeds    180s    ${POLL_INTERVAL}    Deployment Pod Template Annotation Should Not Equal    ${NAMESPACE}    trino-${XTRINODE_NAME}-coordinator    ${COORDINATOR_ROLLOUT_HASH_OUTPUT}    ${coordinator_before_config}
    Wait Until Keyword Succeeds    180s    ${POLL_INTERVAL}    Deployment Pod Template Annotation Should Not Equal    ${NAMESPACE}    trino-${XTRINODE_NAME}-worker    ${WORKER_ROLLOUT_HASH_OUTPUT}    ${worker_before_config}
    Command Should Succeed    kubectl    rollout    status    deployment/trino-${XTRINODE_NAME}-coordinator    -n    ${NAMESPACE}    --timeout=300s
    Command Should Succeed    kubectl    rollout    status    deployment/trino-${XTRINODE_NAME}-worker    -n    ${NAMESPACE}    --timeout=300s

    ${coordinator_before_secret}=    Kubectl Output    get    deployment    trino-${XTRINODE_NAME}-coordinator    -n    ${NAMESPACE}    -o    ${COORDINATOR_ROLLOUT_HASH_OUTPUT}
    ${worker_before_secret}=    Kubectl Output    get    deployment    trino-${XTRINODE_NAME}-worker    -n    ${NAMESPACE}    -o    ${WORKER_ROLLOUT_HASH_OUTPUT}
    ${secret_patch}=    Set Variable    {"data":{"token":"dGhpcmQ=","E2E_ENV_FROM_SECRET":"c2Vjb25k"}}
    Command Should Succeed    kubectl    patch    secret/${MOUNTED_SECRET}    -n    ${NAMESPACE}    --type=merge    -p    ${secret_patch}
    Wait Until Keyword Succeeds    180s    ${POLL_INTERVAL}    Deployment Pod Template Annotation Should Not Equal    ${NAMESPACE}    trino-${XTRINODE_NAME}-coordinator    ${COORDINATOR_ROLLOUT_HASH_OUTPUT}    ${coordinator_before_secret}
    Wait Until Keyword Succeeds    180s    ${POLL_INTERVAL}    Deployment Pod Template Annotation Should Not Equal    ${NAMESPACE}    trino-${XTRINODE_NAME}-worker    ${WORKER_ROLLOUT_HASH_OUTPUT}    ${worker_before_secret}
    Command Should Succeed    kubectl    rollout    status    deployment/trino-${XTRINODE_NAME}-coordinator    -n    ${NAMESPACE}    --timeout=300s
    Command Should Succeed    kubectl    rollout    status    deployment/trino-${XTRINODE_NAME}-worker    -n    ${NAMESPACE}    --timeout=300s

*** Keywords ***
Ensure External Mounted Resources
    ${config_manifest}=    Command Should Succeed    kubectl    create    configmap    ${MOUNTED_CONFIG}    -n    ${NAMESPACE}    --from-literal=settings.properties=version=1    --from-literal=E2E_ENV_FROM_CONFIG=first    --dry-run=client    -o    yaml
    Create File    /tmp/xtrinode-e2e-mounted-config.yaml    ${config_manifest}
    Command Should Succeed    kubectl    apply    -f    /tmp/xtrinode-e2e-mounted-config.yaml
    ${secret_manifest}=    Command Should Succeed    kubectl    create    secret    generic    ${MOUNTED_SECRET}    -n    ${NAMESPACE}    --from-literal=token=first    --from-literal=E2E_ENV_FROM_SECRET=first    --dry-run=client    -o    yaml
    Create File    /tmp/xtrinode-e2e-mounted-secret.yaml    ${secret_manifest}
    Command Should Succeed    kubectl    apply    -f    /tmp/xtrinode-e2e-mounted-secret.yaml

Patch XTrinode With External Mounts
    ${patch}=    Set Variable    {"spec":{"valuesOverlay":{"configMounts":[{"name":"${MOUNTED_CONFIG}","configMap":"${MOUNTED_CONFIG}","path":"/etc/trino/e2e-config"}],"secretMounts":[{"name":"${MOUNTED_SECRET}","secretName":"${MOUNTED_SECRET}","path":"/etc/trino/e2e-secret"}]}}}
    Command Should Succeed    kubectl    patch    xtrinode/${XTRINODE_NAME}    -n    ${NAMESPACE}    --type=merge    -p    ${patch}
    Wait Until Keyword Succeeds    ${WAIT_TIMEOUT}    ${POLL_INTERVAL}    Deployment Should Be Available    ${NAMESPACE}    trino-${XTRINODE_NAME}-coordinator    1
    Wait Until Keyword Succeeds    ${WAIT_TIMEOUT}    ${POLL_INTERVAL}    Deployment Should Be Available    ${NAMESPACE}    trino-${XTRINODE_NAME}-worker    1

Patch XTrinode With External Env Refs
    ${patch}=    Set Variable    {"spec":{"helmChartConfig":{"env":[{"name":"E2E_CONFIG_VALUE","valueFrom":{"configMapKeyRef":{"name":"${MOUNTED_CONFIG}","key":"settings.properties"}}},{"name":"E2E_SECRET_VALUE","valueFrom":{"secretKeyRef":{"name":"${MOUNTED_SECRET}","key":"token"}}}],"envFrom":[{"configMapRef":{"name":"${MOUNTED_CONFIG}"}},{"secretRef":{"name":"${MOUNTED_SECRET}"}}]}}}
    Command Should Succeed    kubectl    patch    xtrinode/${XTRINODE_NAME}    -n    ${NAMESPACE}    --type=merge    -p    ${patch}
    Wait Until Keyword Succeeds    ${WAIT_TIMEOUT}    ${POLL_INTERVAL}    Deployment Should Be Available    ${NAMESPACE}    trino-${XTRINODE_NAME}-coordinator    1
    Wait Until Keyword Succeeds    ${WAIT_TIMEOUT}    ${POLL_INTERVAL}    Deployment Should Be Available    ${NAMESPACE}    trino-${XTRINODE_NAME}-worker    1

Deployment Should Have External Env Refs
    [Arguments]    ${deployment_file}
    JQ Should Match    ${deployment_file}    any(.spec.template.spec.containers[0].env[]?; .name == "E2E_CONFIG_VALUE" and .valueFrom.configMapKeyRef.name == $config and .valueFrom.configMapKeyRef.key == "settings.properties")    --arg    config    ${MOUNTED_CONFIG}
    JQ Should Match    ${deployment_file}    any(.spec.template.spec.containers[0].env[]?; .name == "E2E_SECRET_VALUE" and .valueFrom.secretKeyRef.name == $secret and .valueFrom.secretKeyRef.key == "token")    --arg    secret    ${MOUNTED_SECRET}
    JQ Should Match    ${deployment_file}    any(.spec.template.spec.containers[0].envFrom[]?; .configMapRef.name == $config)    --arg    config    ${MOUNTED_CONFIG}
    JQ Should Match    ${deployment_file}    any(.spec.template.spec.containers[0].envFrom[]?; .secretRef.name == $secret)    --arg    secret    ${MOUNTED_SECRET}

Deployment Should Have External Env Refs In Cluster
    [Arguments]    ${namespace}    ${deployment}
    ${deployment_json}=    Kubectl Output    get    deployment    ${deployment}    -n    ${namespace}    -o    json
    ${deployment_file}=    Set Variable    /tmp/${deployment}-external-env.json
    Create File    ${deployment_file}    ${deployment_json}
    Deployment Should Have External Env Refs    ${deployment_file}

Cleanup Reconciliation Edge Contracts
    ${patch}=    Set Variable    {"spec":{"routing":{"header":"X-Trino-XTrinode=${XTRINODE_NAME}","routingGroup":"${XTRINODE_NAME}","hostnameDomain":null,"hostname":null,"default":false},"valuesOverlay":{"configMounts":null,"secretMounts":null},"helmChartConfig":{"env":null,"envFrom":null}}}
    Run Command Allow Failure    kubectl    patch    xtrinode/${XTRINODE_NAME}    -n    ${NAMESPACE}    --type=merge    -p    ${patch}
    Run Command Allow Failure    kubectl    annotate    xtrinode/${XTRINODE_NAME}    -n    ${NAMESPACE}    xtrinode.analytics.xtrinode.io/allow-breaking-spec-update-
    Run Command Allow Failure    kubectl    delete    configmap/${MOUNTED_CONFIG}    -n    ${NAMESPACE}    --ignore-not-found=true
    Run Command Allow Failure    kubectl    delete    secret/${MOUNTED_SECRET}    -n    ${NAMESPACE}    --ignore-not-found=true
