-- CreateSchema
CREATE SCHEMA IF NOT EXISTS "public";

-- CreateTable
CREATE TABLE "talkwire_profiles" (
    "user_id" VARCHAR(128) NOT NULL,
    "uid" VARCHAR(15) NOT NULL,
    "handle" VARCHAR(64) NOT NULL,
    "handle_key" VARCHAR(64) NOT NULL,
    "handle_changed_at" TIMESTAMPTZ(3),
    "display_name" VARCHAR(64) NOT NULL,
    "avatar_url" VARCHAR(512),
    "created_at" TIMESTAMPTZ(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,
    "updated_at" TIMESTAMPTZ(3) NOT NULL,

    CONSTRAINT "talkwire_profiles_pkey" PRIMARY KEY ("user_id")
);

-- CreateTable
CREATE TABLE "talkwire_contacts" (
    "id" UUID NOT NULL,
    "pair_key" TEXT NOT NULL,
    "requester_id" VARCHAR(128) NOT NULL,
    "recipient_id" VARCHAR(128) NOT NULL,
    "status" VARCHAR(16) NOT NULL,
    "requester_alias" VARCHAR(64),
    "recipient_alias" VARCHAR(64),
    "blocked_by" VARCHAR(128),
    "created_at" TIMESTAMPTZ(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,
    "updated_at" TIMESTAMPTZ(3) NOT NULL,

    CONSTRAINT "talkwire_contacts_pkey" PRIMARY KEY ("id")
);

-- CreateTable
CREATE TABLE "talkwire_admin_audit" (
    "id" UUID NOT NULL,
    "admin_id" VARCHAR(128) NOT NULL,
    "action" VARCHAR(64) NOT NULL,
    "target_user_id" VARCHAR(128),
    "topic" VARCHAR(128),
    "reason" VARCHAR(500),
    "status" VARCHAR(16) NOT NULL,
    "idempotency_key" VARCHAR(128),
    "details" JSONB,
    "created_at" TIMESTAMPTZ(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,
    "completed_at" TIMESTAMPTZ(3),

    CONSTRAINT "talkwire_admin_audit_pkey" PRIMARY KEY ("id")
);

-- CreateTable
CREATE TABLE "talkwire_call_records" (
    "call_session_id" VARCHAR(64) NOT NULL,
    "caller_user_id" VARCHAR(128) NOT NULL,
    "callee_user_id" VARCHAR(128) NOT NULL,
    "requested_type" VARCHAR(8) NOT NULL,
    "call_type" VARCHAR(8) NOT NULL,
    "status" VARCHAR(16) NOT NULL,
    "created_at" TIMESTAMPTZ(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,
    "answered_at" TIMESTAMPTZ(3),
    "upgraded_at" TIMESTAMPTZ(3),
    "ended_at" TIMESTAMPTZ(3),
    "end_reason" VARCHAR(32),
    "duration_sec" INTEGER,

    CONSTRAINT "talkwire_call_records_pkey" PRIMARY KEY ("call_session_id")
);

-- CreateIndex
CREATE UNIQUE INDEX "talkwire_profiles_uid_key" ON "talkwire_profiles"("uid");

-- CreateIndex
CREATE UNIQUE INDEX "talkwire_profiles_handle_key_key" ON "talkwire_profiles"("handle_key");

-- CreateIndex
CREATE UNIQUE INDEX "talkwire_contacts_pair_key_key" ON "talkwire_contacts"("pair_key");

-- CreateIndex
CREATE INDEX "talkwire_contacts_requester_idx" ON "talkwire_contacts"("requester_id", "status", "updated_at");

-- CreateIndex
CREATE INDEX "talkwire_contacts_recipient_idx" ON "talkwire_contacts"("recipient_id", "status", "updated_at");

-- CreateIndex
CREATE UNIQUE INDEX "talkwire_admin_audit_idempotency_key_key" ON "talkwire_admin_audit"("idempotency_key");

-- CreateIndex
CREATE INDEX "talkwire_admin_audit_created_idx" ON "talkwire_admin_audit"("created_at");

-- CreateIndex
CREATE INDEX "talkwire_call_records_created_idx" ON "talkwire_call_records"("created_at", "call_session_id");

-- CreateIndex
CREATE INDEX "talkwire_call_records_caller_idx" ON "talkwire_call_records"("caller_user_id", "created_at");

-- CreateIndex
CREATE INDEX "talkwire_call_records_callee_idx" ON "talkwire_call_records"("callee_user_id", "created_at");

