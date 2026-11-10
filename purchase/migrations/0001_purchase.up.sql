-- erp/purchase: the tables of the whole component (one migration order for all its modules).

-- One queued realignment of the department projection at a time (internal/orgunits): an event of a newer mdm/org
-- epoch waits for it (protocol 02).
CREATE TABLE org_realign (
    single    boolean PRIMARY KEY DEFAULT true CHECK (single),
    queued_at timestamptz NOT NULL DEFAULT now()
);

-- A legal entity's purchase policy; no row is the starting values (approval required, no over-receipt).
CREATE TABLE purchase_policy (
    legal_entity_id      uuid PRIMARY KEY,
    approval             text NOT NULL CHECK (approval IN ('required', 'none')),
    over_receipt_percent numeric(9,4) NOT NULL CHECK (over_receipt_percent >= 0),
    version              bigint NOT NULL DEFAULT 1,
    updated_at           timestamptz NOT NULL DEFAULT now()
);

-- Requisitions (docs/design.md, "申请的状态"); department_path is the org-tree fact of security 07, copied from the
-- department projection and rewritten when the department moves. submitted_by is who submitted (PR03).
CREATE TABLE requisition (
    id              uuid PRIMARY KEY,
    number          text NOT NULL UNIQUE,
    legal_entity_id uuid NOT NULL,
    department_id   uuid,
    department_path text,
    requester_id    text NOT NULL,
    state           text NOT NULL DEFAULT 'draft'
                    CHECK (state IN ('draft', 'submitted', 'approved', 'rejected', 'cancelled', 'closed')),
    reason          text,
    submitted_by    text,
    search          text NOT NULL DEFAULT '',
    search_version  int NOT NULL DEFAULT 0,
    version         bigint NOT NULL DEFAULT 1,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX requisition_list ON requisition (created_at DESC, id);
CREATE INDEX requisition_department ON requisition (department_id);

-- ordered_qty counts what was turned into order lines (PR06), in the line's unit.
CREATE TABLE requisition_line (
    id             uuid PRIMARY KEY,
    requisition_id uuid NOT NULL REFERENCES requisition (id),
    line_no        int NOT NULL,
    product_id     uuid NOT NULL,
    product_code   text NOT NULL,
    product_name   text NOT NULL,
    base_unit      text NOT NULL,
    quantity       numeric(19,6) NOT NULL CHECK (quantity > 0),
    unit           text NOT NULL,
    needed_date    date,
    ordered_qty    numeric(19,6) NOT NULL DEFAULT 0 CHECK (ordered_qty >= 0 AND ordered_qty <= quantity),
    UNIQUE (requisition_id, line_no)
);
