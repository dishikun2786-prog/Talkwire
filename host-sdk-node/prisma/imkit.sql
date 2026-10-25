-- CreateSchema
CREATE SCHEMA IF NOT EXISTS "public";

-- CreateTable
CREATE TABLE "imkit_profiles" (
    "user_id" VARCHAR(128) NOT NULL,
    "uid" VARCHAR(15) NOT NULL,
    "handle" VARCHAR(64) NOT NULL,
    "handle_key" VARCHAR(64) NOT NULL,
    "handle_changed_at" TIMESTAMPTZ(3),
    "display_name" VARCHAR(64) NOT NULL,
    "avatar_url" VARCHAR(512),
    "created_at" TIMESTAMPTZ(3) NOT NULL DEFAULT CURRENT_TIMESTAMP,
    "updated_at" TIMESTAMPTZ(3) NOT NULL,

    CONSTRAINT "imkit_profiles_pkey" PRIMARY KEY ("user_id")
);

-- CreateTable
CREATE TABLE "imkit_contacts" (
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

    CONSTRAINT "imkit_contacts_pkey" PRIMARY KEY ("id")
);

-- CreateTable
CREATE TABLE "imkit_admin_audit" (
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

    CONSTRAINT "imkit_admin_audit_pkey" PRIMARY KEY ("id")
);

-- CreateIndex
CREATE UNIQUE INDEX "imkit_profiles_uid_key" ON "imkit_profiles"("uid");

-- CreateIndex
CREATE UNIQUE INDEX "imkit_profiles_handle_key_key" ON "imkit_profiles"("handle_key");

-- CreateIndex
CREATE UNIQUE INDEX "imkit_contacts_pair_key_key" ON "imkit_contacts"("pair_key");

-- CreateIndex
CREATE INDEX "imkit_contacts_requester_idx" ON "imkit_contacts"("requester_id", "status", "updated_at");

-- CreateIndex
CREATE INDEX "imkit_contacts_recipient_idx" ON "imkit_contacts"("recipient_id", "status", "updated_at");

-- CreateIndex
CREATE UNIQUE INDEX "imkit_admin_audit_idempotency_key_key" ON "imkit_admin_audit"("idempotency_key");

-- CreateIndex
CREATE INDEX "imkit_admin_audit_created_idx" ON "imkit_admin_audit"("created_at");

