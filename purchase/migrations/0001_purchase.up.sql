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

-- Purchase orders (docs/design.md, "订单的状态"); total is net of tax. submitted_by and approved_by are the last
-- submission's and approval's (PO04); reason is the last rejection's.
CREATE TABLE purchase_order (
    id              uuid PRIMARY KEY,
    number          text NOT NULL UNIQUE,
    legal_entity_id uuid NOT NULL,
    supplier_id     uuid NOT NULL,
    supplier_name   text NOT NULL,
    supplier_tax_id text,
    currency        text NOT NULL,
    business_date   date NOT NULL,
    owner_id        text NOT NULL,
    department_id   uuid,
    department_path text,
    state           text NOT NULL DEFAULT 'draft'
                    CHECK (state IN ('draft', 'submitted', 'approved', 'completed', 'closed', 'cancelled')),
    revision        int NOT NULL DEFAULT 1,
    total           numeric(19,4) NOT NULL DEFAULT 0,
    submitted_by    text,
    approved_by     text,
    reason          text,
    search          text NOT NULL DEFAULT '',
    search_version  int NOT NULL DEFAULT 0,
    version         bigint NOT NULL DEFAULT 1,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX purchase_order_list ON purchase_order (created_at DESC, id);
CREATE INDEX purchase_order_department ON purchase_order (department_id);

-- An order line and its progress: receiving and received are base-unit quantities of goods lines (received only from
-- erp/inventory's movements, net of returns); accepted is a service line's, in its unit and amount.
CREATE TABLE order_line (
    id                  uuid PRIMARY KEY,
    order_id            uuid NOT NULL REFERENCES purchase_order (id),
    line_no             int NOT NULL,
    kind                text NOT NULL CHECK (kind IN ('goods', 'service')),
    product_id          uuid NOT NULL,
    product_code        text NOT NULL,
    product_name        text NOT NULL,
    base_unit           text NOT NULL,
    quantity            numeric(19,6) NOT NULL CHECK (quantity > 0),
    unit                text NOT NULL,
    conversion_factor   numeric(24,12) NOT NULL,
    base_quantity       numeric(19,6) NOT NULL,
    unit_price          numeric(19,6) NOT NULL CHECK (unit_price >= 0),
    amount              numeric(19,4) NOT NULL,
    warehouse_id        uuid,
    expected_date       date,
    requisition_line_id uuid REFERENCES requisition_line (id),
    project_ref         text,
    receiving_base_qty  numeric(19,6) NOT NULL DEFAULT 0 CHECK (receiving_base_qty >= 0),
    received_base_qty   numeric(19,6) NOT NULL DEFAULT 0 CHECK (received_base_qty >= 0),
    accepted_qty        numeric(19,6) NOT NULL DEFAULT 0 CHECK (accepted_qty >= 0),
    accepted_amt        numeric(19,4) NOT NULL DEFAULT 0 CHECK (accepted_amt >= 0),
    CHECK ((kind = 'goods') = (warehouse_id IS NOT NULL)),
    UNIQUE (order_id, line_no) DEFERRABLE INITIALLY DEFERRED -- a revision renumbers the lines it keeps
);
CREATE INDEX order_line_requisition ON order_line (requisition_line_id) WHERE requisition_line_id IS NOT NULL;

-- Goods receipts (PO07): receiving until erp/inventory's movements are in, then posted; failed when it refused. The
-- three dimensions are the order's, copied. created_by is who received (the outcome is audited in their name).
CREATE TABLE receipt (
    id              uuid PRIMARY KEY,
    number          text NOT NULL UNIQUE,
    order_id        uuid NOT NULL REFERENCES purchase_order (id),
    order_revision  int NOT NULL,
    legal_entity_id uuid NOT NULL,
    warehouse_id    uuid NOT NULL,
    business_date   date NOT NULL,
    owner_id        text NOT NULL,
    department_id   uuid,
    department_path text,
    state           text NOT NULL DEFAULT 'receiving' CHECK (state IN ('receiving', 'posted', 'failed')),
    reason          text,
    created_by      text NOT NULL,
    version         bigint NOT NULL DEFAULT 1,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX receipt_list ON receipt (created_at DESC, id);
CREATE INDEX receipt_order ON receipt (order_id);
CREATE INDEX receipt_department ON receipt (department_id);
CREATE INDEX receipt_receiving ON receipt (created_at) WHERE state = 'receiving';

-- A receipt line's ID is the receipt line reference erp/inventory's movement and erp/costing carry; quantity is in
-- the order line's unit, base_quantity what erp/inventory takes.
CREATE TABLE receipt_line (
    id            uuid PRIMARY KEY,
    receipt_id    uuid NOT NULL REFERENCES receipt (id),
    line_no       int NOT NULL,
    order_line_id uuid NOT NULL REFERENCES order_line (id),
    product_id    uuid NOT NULL,
    quantity      numeric(19,6) NOT NULL CHECK (quantity > 0),
    base_quantity numeric(19,6) NOT NULL CHECK (base_quantity > 0),
    batch         text,
    serials       text[] NOT NULL DEFAULT '{}',
    UNIQUE (receipt_id, line_no),
    UNIQUE (receipt_id, order_line_id)
);

-- The movements a receipt line received: the only source of the received quantity, one row a movement (its fact_id).
-- returned_base_qty is what purchase returns took back from it.
CREATE TABLE receipt_movement (
    movement_id       uuid PRIMARY KEY,
    receipt_line_id   uuid NOT NULL REFERENCES receipt_line (id),
    order_line_id     uuid NOT NULL REFERENCES order_line (id),
    base_quantity     numeric(19,6) NOT NULL CHECK (base_quantity > 0),
    returned_base_qty numeric(19,6) NOT NULL DEFAULT 0 CHECK (returned_base_qty >= 0 AND returned_base_qty <= base_quantity),
    recorded_at       timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX receipt_movement_line ON receipt_movement (receipt_line_id);

-- Every incremental fact this component published, by its ID: what FactService.Read answers (protocol 05, common
-- rules: a consumer that missed one reads it back).
CREATE TABLE purchase_fact (
    fact_id     uuid NOT NULL,
    type        text NOT NULL,
    data        jsonb NOT NULL,
    recorded_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (fact_id, type)
);
