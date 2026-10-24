--
-- PostgreSQL database dump
--

\restrict w56igF7MbRaoqvW75ciU5tPMXU2e4vQwt97vcpig9BPIsu6jquRyTKPte1TNgkl

-- Dumped from database version 17.11
-- Dumped by pg_dump version 17.11

SET statement_timeout = 0;
SET lock_timeout = 0;
SET idle_in_transaction_session_timeout = 0;
SET transaction_timeout = 0;
SET client_encoding = 'UTF8';
SET standard_conforming_strings = on;
SELECT pg_catalog.set_config('search_path', '', false);
SET check_function_bodies = false;
SET xmloption = content;
SET client_min_messages = warning;
SET row_security = off;

SET default_tablespace = '';

SET default_table_access_method = heap;

--
-- Name: im_e2e_envelopes; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.im_e2e_envelopes (
    id bigint NOT NULL,
    topic character varying(128) NOT NULL,
    to_uid character varying(64) NOT NULL,
    from_uid character varying(64) NOT NULL,
    kind character varying(24) NOT NULL,
    payload text DEFAULT ''::text NOT NULL,
    msg_type integer DEFAULT 0 NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    expires_at timestamp with time zone NOT NULL
);


--
-- Name: im_e2e_envelopes_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.im_e2e_envelopes_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


--
-- Name: im_e2e_envelopes_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.im_e2e_envelopes_id_seq OWNED BY public.im_e2e_envelopes.id;


--
-- Name: im_e2e_identities; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.im_e2e_identities (
    uid character varying(64) NOT NULL,
    device_id integer DEFAULT 1 NOT NULL,
    registration_id integer NOT NULL,
    identity_key text NOT NULL,
    signed_pre_key_id integer NOT NULL,
    signed_pre_key text NOT NULL,
    signed_pre_key_signature text NOT NULL,
    kyber_pre_key_id integer NOT NULL,
    kyber_pre_key text NOT NULL,
    kyber_pre_key_signature text NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL
);


--
-- Name: im_e2e_one_time_pre_keys; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.im_e2e_one_time_pre_keys (
    id bigint NOT NULL,
    uid character varying(64) NOT NULL,
    key_id integer NOT NULL,
    pre_key text NOT NULL,
    consumed boolean DEFAULT false NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL
);


--
-- Name: im_e2e_one_time_pre_keys_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.im_e2e_one_time_pre_keys_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


--
-- Name: im_e2e_one_time_pre_keys_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.im_e2e_one_time_pre_keys_id_seq OWNED BY public.im_e2e_one_time_pre_keys.id;


--
-- Name: im_fcm_tokens; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.im_fcm_tokens (
    uid character varying(64) NOT NULL,
    token text NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL
);


--
-- Name: im_group_invite_tokens; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.im_group_invite_tokens (
    topic character varying(64) NOT NULL,
    token character varying(64) NOT NULL,
    created_by character varying(64) NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    expires_at timestamp with time zone NOT NULL
);


--
-- Name: im_group_join_requests; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.im_group_join_requests (
    id bigint NOT NULL,
    topic character varying(64) NOT NULL,
    requester_uid character varying(64) NOT NULL,
    message character varying(120) DEFAULT ''::character varying NOT NULL,
    status character varying(16) DEFAULT 'pending'::character varying NOT NULL,
    reviewed_by character varying(64) DEFAULT ''::character varying NOT NULL,
    reviewed_at timestamp with time zone,
    created_at timestamp with time zone DEFAULT now() NOT NULL
);


--
-- Name: im_group_join_requests_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.im_group_join_requests_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


--
-- Name: im_group_join_requests_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.im_group_join_requests_id_seq OWNED BY public.im_group_join_requests.id;


--
-- Name: im_group_settings; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.im_group_settings (
    topic character varying(64) NOT NULL,
    ai_qa_enabled boolean DEFAULT false NOT NULL,
    ai_kb_ids jsonb DEFAULT '[]'::jsonb NOT NULL,
    ai_doc_ids jsonb DEFAULT '[]'::jsonb NOT NULL,
    ai_daily_quota integer DEFAULT 50 NOT NULL,
    ai_cooldown_sec integer DEFAULT 5 NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_by character varying(64) NOT NULL,
    require_join_approval boolean DEFAULT false NOT NULL
);


--
-- Name: im_messages; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.im_messages (
    id bigint NOT NULL,
    topic character varying(128) NOT NULL,
    seq integer NOT NULL,
    from_uid character varying(64) NOT NULL,
    content text DEFAULT ''::text NOT NULL,
    content_type character varying(64) DEFAULT 'text'::character varying NOT NULL,
    file_url text,
    file_name character varying(255),
    extra jsonb,
    duration integer DEFAULT 0,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    forward_from_msg_id bigint,
    forward_from_topic character varying(64) DEFAULT NULL::character varying,
    forward_chain_depth smallint DEFAULT 0 NOT NULL,
    sender_kind character varying(16) DEFAULT 'user'::character varying NOT NULL,
    client_msg_id character varying(64) DEFAULT NULL::character varying,
    recalled_at timestamp with time zone
);


--
-- Name: im_messages_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.im_messages_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


--
-- Name: im_messages_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.im_messages_id_seq OWNED BY public.im_messages.id;


--
-- Name: im_pending_clears; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.im_pending_clears (
    id bigint NOT NULL,
    topic character varying(128) NOT NULL,
    uid character varying(64) NOT NULL,
    wire_topic character varying(128) NOT NULL,
    clear_version integer NOT NULL,
    clear_before_seq integer NOT NULL,
    from_uid character varying(64) DEFAULT ''::character varying NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL
);


--
-- Name: im_pending_clears_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.im_pending_clears_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


--
-- Name: im_pending_clears_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.im_pending_clears_id_seq OWNED BY public.im_pending_clears.id;


--
-- Name: im_pending_group_events; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.im_pending_group_events (
    id bigint NOT NULL,
    topic character varying(128) NOT NULL,
    uid character varying(64) NOT NULL,
    kind character varying(24) NOT NULL,
    group_name character varying(255) DEFAULT ''::character varying NOT NULL,
    from_uid character varying(64) DEFAULT ''::character varying NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL
);


--
-- Name: im_pending_group_events_id_seq; Type: SEQUENCE; Schema: public; Owner: -
--

CREATE SEQUENCE public.im_pending_group_events_id_seq
    START WITH 1
    INCREMENT BY 1
    NO MINVALUE
    NO MAXVALUE
    CACHE 1;


--
-- Name: im_pending_group_events_id_seq; Type: SEQUENCE OWNED BY; Schema: public; Owner: -
--

ALTER SEQUENCE public.im_pending_group_events_id_seq OWNED BY public.im_pending_group_events.id;


--
-- Name: im_read_cursors; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.im_read_cursors (
    topic character varying(128) NOT NULL,
    uid character varying(64) NOT NULL,
    last_read_seq integer DEFAULT 0 NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL
);


--
-- Name: im_topic_members; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.im_topic_members (
    topic character varying(128) NOT NULL,
    uid character varying(64) NOT NULL,
    role character varying(20) DEFAULT 'member'::character varying NOT NULL,
    joined_at timestamp with time zone DEFAULT now() NOT NULL
);


--
-- Name: im_topics; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.im_topics (
    topic character varying(128) NOT NULL,
    is_group boolean DEFAULT false NOT NULL,
    name character varying(255) DEFAULT ''::character varying,
    owner_uid character varying(64) DEFAULT ''::character varying NOT NULL,
    last_seq integer DEFAULT 0 NOT NULL,
    clear_version integer DEFAULT 0 NOT NULL,
    clear_before_seq integer DEFAULT 0 NOT NULL,
    cleared_at timestamp with time zone,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    updated_at timestamp with time zone DEFAULT now() NOT NULL,
    encrypted boolean DEFAULT false NOT NULL
);


--
-- Name: im_users; Type: TABLE; Schema: public; Owner: -
--

CREATE TABLE public.im_users (
    app_user_id character varying(64) NOT NULL,
    im_user_id character varying(64) NOT NULL,
    created_at timestamp with time zone DEFAULT now() NOT NULL,
    last_seen_at timestamp with time zone
);


--
-- Name: im_e2e_envelopes id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.im_e2e_envelopes ALTER COLUMN id SET DEFAULT nextval('public.im_e2e_envelopes_id_seq'::regclass);


--
-- Name: im_e2e_one_time_pre_keys id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.im_e2e_one_time_pre_keys ALTER COLUMN id SET DEFAULT nextval('public.im_e2e_one_time_pre_keys_id_seq'::regclass);


--
-- Name: im_group_join_requests id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.im_group_join_requests ALTER COLUMN id SET DEFAULT nextval('public.im_group_join_requests_id_seq'::regclass);


--
-- Name: im_messages id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.im_messages ALTER COLUMN id SET DEFAULT nextval('public.im_messages_id_seq'::regclass);


--
-- Name: im_pending_clears id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.im_pending_clears ALTER COLUMN id SET DEFAULT nextval('public.im_pending_clears_id_seq'::regclass);


--
-- Name: im_pending_group_events id; Type: DEFAULT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.im_pending_group_events ALTER COLUMN id SET DEFAULT nextval('public.im_pending_group_events_id_seq'::regclass);


--
-- Name: im_e2e_envelopes im_e2e_envelopes_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.im_e2e_envelopes
    ADD CONSTRAINT im_e2e_envelopes_pkey PRIMARY KEY (id);


--
-- Name: im_e2e_identities im_e2e_identities_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.im_e2e_identities
    ADD CONSTRAINT im_e2e_identities_pkey PRIMARY KEY (uid);


--
-- Name: im_e2e_one_time_pre_keys im_e2e_one_time_pre_keys_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.im_e2e_one_time_pre_keys
    ADD CONSTRAINT im_e2e_one_time_pre_keys_pkey PRIMARY KEY (id);


--
-- Name: im_e2e_one_time_pre_keys im_e2e_one_time_pre_keys_uid_key_id_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.im_e2e_one_time_pre_keys
    ADD CONSTRAINT im_e2e_one_time_pre_keys_uid_key_id_key UNIQUE (uid, key_id);


--
-- Name: im_fcm_tokens im_fcm_tokens_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.im_fcm_tokens
    ADD CONSTRAINT im_fcm_tokens_pkey PRIMARY KEY (token);


--
-- Name: im_group_invite_tokens im_group_invite_tokens_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.im_group_invite_tokens
    ADD CONSTRAINT im_group_invite_tokens_pkey PRIMARY KEY (topic);


--
-- Name: im_group_join_requests im_group_join_requests_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.im_group_join_requests
    ADD CONSTRAINT im_group_join_requests_pkey PRIMARY KEY (id);


--
-- Name: im_group_settings im_group_settings_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.im_group_settings
    ADD CONSTRAINT im_group_settings_pkey PRIMARY KEY (topic);


--
-- Name: im_messages im_messages_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.im_messages
    ADD CONSTRAINT im_messages_pkey PRIMARY KEY (id);


--
-- Name: im_messages im_messages_topic_seq_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.im_messages
    ADD CONSTRAINT im_messages_topic_seq_key UNIQUE (topic, seq);


--
-- Name: im_pending_clears im_pending_clears_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.im_pending_clears
    ADD CONSTRAINT im_pending_clears_pkey PRIMARY KEY (id);


--
-- Name: im_pending_clears im_pending_clears_topic_uid_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.im_pending_clears
    ADD CONSTRAINT im_pending_clears_topic_uid_key UNIQUE (topic, uid);


--
-- Name: im_pending_group_events im_pending_group_events_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.im_pending_group_events
    ADD CONSTRAINT im_pending_group_events_pkey PRIMARY KEY (id);


--
-- Name: im_pending_group_events im_pending_group_events_topic_uid_kind_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.im_pending_group_events
    ADD CONSTRAINT im_pending_group_events_topic_uid_kind_key UNIQUE (topic, uid, kind);


--
-- Name: im_read_cursors im_read_cursors_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.im_read_cursors
    ADD CONSTRAINT im_read_cursors_pkey PRIMARY KEY (topic, uid);


--
-- Name: im_topic_members im_topic_members_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.im_topic_members
    ADD CONSTRAINT im_topic_members_pkey PRIMARY KEY (topic, uid);


--
-- Name: im_topics im_topics_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.im_topics
    ADD CONSTRAINT im_topics_pkey PRIMARY KEY (topic);


--
-- Name: im_users im_users_im_user_id_key; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.im_users
    ADD CONSTRAINT im_users_im_user_id_key UNIQUE (im_user_id);


--
-- Name: im_users im_users_pkey; Type: CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.im_users
    ADD CONSTRAINT im_users_pkey PRIMARY KEY (app_user_id);


--
-- Name: idx_im_e2e_envelopes_expires; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_im_e2e_envelopes_expires ON public.im_e2e_envelopes USING btree (expires_at);


--
-- Name: idx_im_e2e_envelopes_to; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_im_e2e_envelopes_to ON public.im_e2e_envelopes USING btree (to_uid, id);


--
-- Name: idx_im_e2e_otpk_available; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_im_e2e_otpk_available ON public.im_e2e_one_time_pre_keys USING btree (uid, id) WHERE (consumed = false);


--
-- Name: idx_im_group_invite_tokens_token; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX idx_im_group_invite_tokens_token ON public.im_group_invite_tokens USING btree (token);


--
-- Name: idx_im_group_join_requests_pending; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX idx_im_group_join_requests_pending ON public.im_group_join_requests USING btree (topic, requester_uid) WHERE ((status)::text = 'pending'::text);


--
-- Name: idx_im_group_join_requests_topic; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_im_group_join_requests_topic ON public.im_group_join_requests USING btree (topic, status);


--
-- Name: idx_im_messages_topic; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_im_messages_topic ON public.im_messages USING btree (topic, created_at DESC);


--
-- Name: idx_im_messages_topic_client_msg_id; Type: INDEX; Schema: public; Owner: -
--

CREATE UNIQUE INDEX idx_im_messages_topic_client_msg_id ON public.im_messages USING btree (topic, client_msg_id) WHERE (client_msg_id IS NOT NULL);


--
-- Name: idx_im_messages_topic_seq; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_im_messages_topic_seq ON public.im_messages USING btree (topic, seq DESC);


--
-- Name: idx_im_pending_clears_uid; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_im_pending_clears_uid ON public.im_pending_clears USING btree (uid);


--
-- Name: idx_im_pending_group_events_uid; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_im_pending_group_events_uid ON public.im_pending_group_events USING btree (uid);


--
-- Name: idx_im_read_cursors_topic; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_im_read_cursors_topic ON public.im_read_cursors USING btree (topic);


--
-- Name: idx_im_topic_members_uid; Type: INDEX; Schema: public; Owner: -
--

CREATE INDEX idx_im_topic_members_uid ON public.im_topic_members USING btree (uid);


--
-- Name: im_group_invite_tokens im_group_invite_tokens_topic_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.im_group_invite_tokens
    ADD CONSTRAINT im_group_invite_tokens_topic_fkey FOREIGN KEY (topic) REFERENCES public.im_topics(topic) ON DELETE CASCADE;


--
-- Name: im_group_settings im_group_settings_topic_fkey; Type: FK CONSTRAINT; Schema: public; Owner: -
--

ALTER TABLE ONLY public.im_group_settings
    ADD CONSTRAINT im_group_settings_topic_fkey FOREIGN KEY (topic) REFERENCES public.im_topics(topic) ON DELETE CASCADE;


--
-- PostgreSQL database dump complete
--

\unrestrict w56igF7MbRaoqvW75ciU5tPMXU2e4vQwt97vcpig9BPIsu6jquRyTKPte1TNgkl

