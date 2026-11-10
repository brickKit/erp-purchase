from google.protobuf.internal import containers as _containers
from google.protobuf import descriptor as _descriptor
from google.protobuf import message as _message
from collections.abc import Iterable as _Iterable, Mapping as _Mapping
from typing import ClassVar as _ClassVar, Optional as _Optional, Union as _Union

DESCRIPTOR: _descriptor.FileDescriptor

class BasisRequest(_message.Message):
    __slots__ = ("order_id", "order_line_ids")
    ORDER_ID_FIELD_NUMBER: _ClassVar[int]
    ORDER_LINE_IDS_FIELD_NUMBER: _ClassVar[int]
    order_id: str
    order_line_ids: _containers.RepeatedScalarFieldContainer[str]
    def __init__(self, order_id: _Optional[str] = ..., order_line_ids: _Optional[_Iterable[str]] = ...) -> None: ...

class ReceiptMovement(_message.Message):
    __slots__ = ("movement_id", "receipt_line_id", "received_base_quantity", "returned_base_quantity")
    MOVEMENT_ID_FIELD_NUMBER: _ClassVar[int]
    RECEIPT_LINE_ID_FIELD_NUMBER: _ClassVar[int]
    RECEIVED_BASE_QUANTITY_FIELD_NUMBER: _ClassVar[int]
    RETURNED_BASE_QUANTITY_FIELD_NUMBER: _ClassVar[int]
    movement_id: str
    receipt_line_id: str
    received_base_quantity: str
    returned_base_quantity: str
    def __init__(self, movement_id: _Optional[str] = ..., receipt_line_id: _Optional[str] = ..., received_base_quantity: _Optional[str] = ..., returned_base_quantity: _Optional[str] = ...) -> None: ...

class BasisLine(_message.Message):
    __slots__ = ("order_id", "order_line_id", "order_revision", "legal_entity_id", "supplier_id", "kind", "product_id", "unit", "ordered_quantity", "received_base_quantity", "accepted_quantity", "accepted_amount", "unit_price", "currency", "movements", "order_state")
    ORDER_ID_FIELD_NUMBER: _ClassVar[int]
    ORDER_LINE_ID_FIELD_NUMBER: _ClassVar[int]
    ORDER_REVISION_FIELD_NUMBER: _ClassVar[int]
    LEGAL_ENTITY_ID_FIELD_NUMBER: _ClassVar[int]
    SUPPLIER_ID_FIELD_NUMBER: _ClassVar[int]
    KIND_FIELD_NUMBER: _ClassVar[int]
    PRODUCT_ID_FIELD_NUMBER: _ClassVar[int]
    UNIT_FIELD_NUMBER: _ClassVar[int]
    ORDERED_QUANTITY_FIELD_NUMBER: _ClassVar[int]
    RECEIVED_BASE_QUANTITY_FIELD_NUMBER: _ClassVar[int]
    ACCEPTED_QUANTITY_FIELD_NUMBER: _ClassVar[int]
    ACCEPTED_AMOUNT_FIELD_NUMBER: _ClassVar[int]
    UNIT_PRICE_FIELD_NUMBER: _ClassVar[int]
    CURRENCY_FIELD_NUMBER: _ClassVar[int]
    MOVEMENTS_FIELD_NUMBER: _ClassVar[int]
    ORDER_STATE_FIELD_NUMBER: _ClassVar[int]
    order_id: str
    order_line_id: str
    order_revision: int
    legal_entity_id: str
    supplier_id: str
    kind: str
    product_id: str
    unit: str
    ordered_quantity: str
    received_base_quantity: str
    accepted_quantity: str
    accepted_amount: str
    unit_price: str
    currency: str
    movements: _containers.RepeatedCompositeFieldContainer[ReceiptMovement]
    order_state: str
    def __init__(self, order_id: _Optional[str] = ..., order_line_id: _Optional[str] = ..., order_revision: _Optional[int] = ..., legal_entity_id: _Optional[str] = ..., supplier_id: _Optional[str] = ..., kind: _Optional[str] = ..., product_id: _Optional[str] = ..., unit: _Optional[str] = ..., ordered_quantity: _Optional[str] = ..., received_base_quantity: _Optional[str] = ..., accepted_quantity: _Optional[str] = ..., accepted_amount: _Optional[str] = ..., unit_price: _Optional[str] = ..., currency: _Optional[str] = ..., movements: _Optional[_Iterable[_Union[ReceiptMovement, _Mapping]]] = ..., order_state: _Optional[str] = ...) -> None: ...

class BasisResponse(_message.Message):
    __slots__ = ("lines",)
    LINES_FIELD_NUMBER: _ClassVar[int]
    lines: _containers.RepeatedCompositeFieldContainer[BasisLine]
    def __init__(self, lines: _Optional[_Iterable[_Union[BasisLine, _Mapping]]] = ...) -> None: ...

class ReadRequest(_message.Message):
    __slots__ = ("fact_ids",)
    FACT_IDS_FIELD_NUMBER: _ClassVar[int]
    fact_ids: _containers.RepeatedScalarFieldContainer[str]
    def __init__(self, fact_ids: _Optional[_Iterable[str]] = ...) -> None: ...

class Fact(_message.Message):
    __slots__ = ("fact_id", "type", "data")
    FACT_ID_FIELD_NUMBER: _ClassVar[int]
    TYPE_FIELD_NUMBER: _ClassVar[int]
    DATA_FIELD_NUMBER: _ClassVar[int]
    fact_id: str
    type: str
    data: bytes
    def __init__(self, fact_id: _Optional[str] = ..., type: _Optional[str] = ..., data: _Optional[bytes] = ...) -> None: ...

class ReadResponse(_message.Message):
    __slots__ = ("facts",)
    FACTS_FIELD_NUMBER: _ClassVar[int]
    facts: _containers.RepeatedCompositeFieldContainer[Fact]
    def __init__(self, facts: _Optional[_Iterable[_Union[Fact, _Mapping]]] = ...) -> None: ...
