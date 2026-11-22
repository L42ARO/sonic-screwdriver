"""Patient endpoints of the clinic portal, backed by DentaSoft."""
from fastapi import APIRouter, HTTPException
from pydantic import BaseModel, Field

from dentasoft import BalanceChange, Patient, lookup_patient, update_balance

router = APIRouter(prefix="/patients", tags=["patients"])


@router.get("/{patient_id}")
async def get_patient(patient_id: str) -> Patient:
    patient = await lookup_patient(patient_id)
    if not patient.found:
        raise HTTPException(404, detail={"code": "not_found", "message": f"no patient {patient_id}"})
    return patient


class SetBalance(BaseModel):
    balance: float = Field(ge=0)
    # Lets callers retry safely: the same request_id never edits DentaSoft twice.
    request_id: str | None = None


@router.put("/{patient_id}/balance")
async def set_balance(patient_id: str, body: SetBalance) -> BalanceChange:
    return await update_balance.with_options(idempotency_key=body.request_id)(patient_id, body.balance)
