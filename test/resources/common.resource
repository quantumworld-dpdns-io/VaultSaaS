*** Settings ***
Documentation       Shared keywords: building and starting the local stack
...                 (mock BitGo -> Go core -> wrangler edge -> Vite dashboard).
Library             Process
Library             OperatingSystem
Library             String
Library             Collections
Library             RequestsLibrary


*** Variables ***
${ROOT}                 ${CURDIR}/../..
${CORE_DIR}             ${ROOT}/services/core
${RESULTS}              ${CURDIR}/../results
${BIN}                  ${RESULTS}/bin
${MOCK_PORT}            18090
${CORE_PORT}            18080
${EDGE_PORT}            18787
${WEB_PORT}             15173
${BITGO_TOKEN}          robot-bitgo-token
${MOCK_URL}             http://127.0.0.1:${MOCK_PORT}
${CORE_URL}             http://127.0.0.1:${CORE_PORT}
${EDGE_URL}             http://127.0.0.1:${EDGE_PORT}
${WEB_URL}              http://127.0.0.1:${WEB_PORT}
${COMPOSE}              docker compose -f ${ROOT}/docker-compose.local.yml
${ROBOT_DB}             vaultops_robot
${OWNER_DB_URL}         postgres://postgres:postgres@127.0.0.1:54322/${ROBOT_DB}?sslmode=disable
${APP_DB_PASSWORD}      robot-app-password-123
${APP_DB_URL}           postgres://vaultops_app:${APP_DB_PASSWORD}@127.0.0.1:54322/${ROBOT_DB}?sslmode=disable
${SQS_ENDPOINT}         http://127.0.0.1:9324
${QUEUE_URL}            ${SQS_ENDPOINT}/000000000000/vaultops-local-webhook-events
${DLQ_URL}              ${SQS_ENDPOINT}/000000000000/vaultops-local-webhook-events-dlq
${ELASTICMQ_JAR}        ${ROOT}/.tools/elasticmq-server-1.7.1.jar
${AUTH_PORT}            18095
${AUTH_URL}             http://127.0.0.1:${AUTH_PORT}
${SIWE_DOMAIN}          127.0.0.1:${WEB_PORT}
# test-only keyring: base64("0123456789abcdef0123456789abcdef")
${TOKEN_ENC_KEYS}       1:MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=
${ANCHOR_DIR}           ${RESULTS}/anchors


*** Keywords ***
Run And Expect Success
    [Documentation]    Runs a command in the repo and fails with its output if it exits non-zero.
    [Arguments]    ${command}    ${cwd}=${ROOT}    ${timeout}=600s
    ${result}=    Run Process    bash    -c    ${command}    cwd=${cwd}    timeout=${timeout}
    ...    stderr=STDOUT    on_timeout=kill
    Log    ${result.stdout}
    Should Be Equal As Integers    ${result.rc}    0    msg=`${command}` failed:\n${result.stdout}
    RETURN    ${result.stdout}

Build Core Binaries
    Create Directory    ${BIN}
    Run And Expect Success    for c in local mockbitgo vaultctl mockauth devsign; do go build -o ${BIN}/$c ./cmd/$c || exit 1; done; mv ${BIN}/local ${BIN}/core-local
    ...    cwd=${CORE_DIR}

Wait For Http
    [Arguments]    ${url}    ${timeout}=90s
    Wait Until Keyword Succeeds    ${timeout}    1s    Url Should Respond    ${url}

Url Should Respond
    [Arguments]    ${url}
    ${resp}=    GET    ${url}    expected_status=anything    timeout=2
    Should Be True    ${resp.status_code} < 500    ${url} answered ${resp.status_code}

Start Mock BitGo
    Start Process    ${BIN}/mockbitgo    alias=mockbitgo    cwd=${ROOT}
    ...    env:MOCK_BITGO_ADDR=127.0.0.1:${MOCK_PORT}    env:MOCK_BITGO_TOKEN=${BITGO_TOKEN}
    ...    stdout=${RESULTS}/mockbitgo.log    stderr=STDOUT
    Wait For Http    ${MOCK_URL}/__mock/requests

Start Core
    [Documentation]    Core with database, SQS poller, token keyring and SIWE domain.
    ...    The local-only BITGO_TOKEN fallback serves orgs without their own connection.
    [Arguments]    ${port}=${CORE_PORT}    ${token}=${EMPTY}    ${alias}=core    &{extra_env}
    Start Process    ${BIN}/core-local    alias=${alias}    cwd=${ROOT}
    ...    env:CORE_ADDR=127.0.0.1:${port}    env:BITGO_BASE_URL=${MOCK_URL}    env:BITGO_TOKEN=${token}
    ...    env:VAULTOPS_ENV=local    env:DATABASE_URL=${APP_DB_URL}    env:TOKEN_ENC_KEYS=${TOKEN_ENC_KEYS}
    ...    env:SIWE_DOMAIN=${SIWE_DOMAIN}    &{extra_env}
    ...    stdout=${RESULTS}/${alias}.log    stderr=STDOUT
    Wait For Http    http://127.0.0.1:${port}/healthz

Start Edge
    Start Process    npx    wrangler    dev    --local    --ip    127.0.0.1    --port    ${EDGE_PORT}
    ...    --var    CORE_URL:${CORE_URL}    --var    ALLOWED_ORIGINS:${WEB_URL}    --var    SQS_QUEUE_URL:${QUEUE_URL}
    ...    --var    SUPABASE_ISSUER:${AUTH_URL}/auth/v1    --var    SUPABASE_JWKS_URL:${AUTH_URL}/auth/v1/.well-known/jwks.json
    ...    alias=edge    cwd=${ROOT}/apps/edge
    ...    env:WRANGLER_SEND_METRICS=false    env:CI=1
    ...    stdout=${RESULTS}/edge.log    stderr=STDOUT
    Wait For Http    ${EDGE_URL}/healthz    timeout=120s

Start Web
    Run And Expect Success    npx tsc --noEmit && VITE_API_BASE=${EDGE_URL} VITE_AUTH_MODE=mock VITE_MOCK_AUTH_URL=${AUTH_URL} npx vite build --outDir ${RESULTS}/web-dist --emptyOutDir
    ...    cwd=${ROOT}/apps/web
    Start Process    npx    vite    preview    --outDir    ${RESULTS}/web-dist    --host    127.0.0.1
    ...    --port    ${WEB_PORT}    --strictPort
    ...    alias=web    cwd=${ROOT}/apps/web
    ...    stdout=${RESULTS}/web.log    stderr=STDOUT
    Wait For Http    ${WEB_URL}/

Start Postgres
    Run And Expect Success    ${COMPOSE} up -d --wait postgres    timeout=180s

Psql
    [Documentation]    Runs SQL as the owner inside the postgres container; returns trimmed output.
    [Arguments]    ${sql}    ${db}=${ROBOT_DB}
    ${r}=    Run Process    ${COMPOSE} exec -T postgres psql -U postgres -d ${db} -v ON_ERROR_STOP\=1 -tAc "${sql}"
    ...    shell=True    stderr=STDOUT
    Should Be Equal As Integers    ${r.rc}    0    msg=psql failed: ${r.stdout}
    RETURN    ${r.stdout.strip()}

Create Fresh Robot Database
    Psql    DROP DATABASE IF EXISTS ${ROBOT_DB} WITH (FORCE)    db=postgres
    Psql    CREATE DATABASE ${ROBOT_DB}    db=postgres
    Run And Expect Success    ${COMPOSE} exec -T postgres psql -U postgres -d ${ROBOT_DB} -v ON_ERROR_STOP=1 -q -f /docker-entrypoint-initdb.d/00_supabase_stub.sql
    Run And Expect Success    DATABASE_OWNER_URL='${OWNER_DB_URL}' VAULTOPS_APP_DB_PASSWORD='${APP_DB_PASSWORD}' ${BIN}/vaultctl migrate --dir db/migrations --set-app-password

Create Organization
    [Documentation]    Returns (org_id, webhook_token).
    [Arguments]    ${name}
    ${out}=    Run And Expect Success    DATABASE_URL='${APP_DB_URL}' ${BIN}/vaultctl org-create --name '${name}'
    ${org}=    Get Regexp Matches    ${out}    org_id=(\\S+)    1
    ${token}=    Get Regexp Matches    ${out}    webhook_token=(\\S+)    1
    RETURN    ${org}[0]    ${token}[0]

Start ElasticMQ
    File Should Exist    ${ELASTICMQ_JAR}    Run `make tools` to download ElasticMQ.
    Start Process    java    -Dconfig.file\=${ROOT}/db/local/elasticmq.conf    -jar    ${ELASTICMQ_JAR}
    ...    alias=elasticmq    cwd=${ROOT}    stdout=${RESULTS}/elasticmq.log    stderr=STDOUT
    Wait For Http    ${SQS_ENDPOINT}/?Action=ListQueues

Queue Depth
    [Documentation]    Visible + in-flight messages on a queue.
    [Arguments]    ${queue_url}
    ${body}=    Create Dictionary    QueueUrl=${queue_url}    AttributeNames=${{["All"]}}
    ${headers}=    Create Dictionary    X-Amz-Target=AmazonSQS.GetQueueAttributes    Content-Type=application/x-amz-json-1.0
    ${resp}=    POST    ${SQS_ENDPOINT}/    data=${{json.dumps($body)}}    headers=${headers}    expected_status=200
    ${a}=    Set Variable    ${resp.json()}[Attributes]
    RETURN    ${{int($a["ApproximateNumberOfMessages"]) + int($a["ApproximateNumberOfMessagesNotVisible"])}}

Start Mock Auth
    Start Process    ${BIN}/mockauth    alias=mockauth    cwd=${ROOT}
    ...    env:MOCK_AUTH_ADDR=127.0.0.1:${AUTH_PORT}    env:MOCK_AUTH_DB_URL=${OWNER_DB_URL}
    ...    stdout=${RESULTS}/mockauth.log    stderr=STDOUT
    Wait For Http    ${AUTH_URL}/auth/v1/.well-known/jwks.json

Start Full Stack
    [Documentation]    Fresh database + every local stand-in, then core and edge (and the dashboard).
    [Arguments]    ${with_web}=${False}    ${fallback_token}=${EMPTY}
    Build Core Binaries
    Start Postgres
    Create Fresh Robot Database
    Start ElasticMQ
    Start Mock BitGo
    Start Mock Auth
    Start Core    token=${fallback_token}    env:LOCAL_QUEUE_URL=${QUEUE_URL}
    Start Edge
    IF    ${with_web}    Start Web

Get Token
    [Documentation]    Signs in through mockauth (creates the auth user) and returns the access token.
    [Arguments]    ${email}
    ${resp}=    POST    ${AUTH_URL}/__mock/token    json=${{{"email": $email}}}    expected_status=200
    RETURN    ${resp.json()}[access_token]

User Id Of
    [Arguments]    ${email}
    ${resp}=    POST    ${AUTH_URL}/__mock/token    json=${{{"email": $email}}}    expected_status=200
    RETURN    ${resp.json()}[user][id]

Api
    [Documentation]    Calls the edge as a signed-in user; returns the response.
    [Arguments]    ${method}    ${path}    ${token}    ${org}=${None}    ${body}=${None}    ${expected}=200
    ...    &{extra_headers}
    ${headers}=    Create Dictionary    Authorization=Bearer ${token}    &{extra_headers}
    IF    $org is not None    Set To Dictionary    ${headers}    X-Org-Id=${org}
    IF    '${method}' == 'GET'
        ${resp}=    GET    ${EDGE_URL}${path}    headers=${headers}    expected_status=${expected}
    ELSE IF    '${method}' == 'POST'
        ${resp}=    POST    ${EDGE_URL}${path}    headers=${headers}    json=${body}    expected_status=${expected}
    ELSE
        ${resp}=    PUT    ${EDGE_URL}${path}    headers=${headers}    json=${body}    expected_status=${expected}
    END
    RETURN    ${resp}

Create Org Via Api
    [Documentation]    Returns (org_id, webhook_token).
    [Arguments]    ${token}    ${name}
    ${resp}=    Api    POST    /api/v1/orgs    ${token}    body=${{{"name": $name}}}    expected=201
    RETURN    ${resp.json()}[id]    ${resp.json()}[webhookToken]

Connect BitGo
    [Arguments]    ${token}    ${org}
    ${resp}=    Api    PUT    /api/v1/org/bitgo-connection    ${token}    ${org}    body=${{{"token": $BITGO_TOKEN, "enterpriseId": "ent_demo"}}}
    RETURN    ${resp}

Reset Mock BitGo
    POST    ${MOCK_URL}/__mock/reset    expected_status=200

Stop Local Stack
    Terminate All Processes    kill=True
