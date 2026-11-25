"""DentaSoft, the clinic's 1998 records program, as typed async functions.

DentaSoft has no API. Each function below is carried out by an agent that looks at the
screen of the clinic's PC and uses the mouse and keyboard. The signature is the input,
the return type is the output, and the docstring tells the agent what to do.
"""
import os
import sys
import ntpath
from pathlib import Path
from typing import Annotated

from pydantic import BaseModel, Field
from sonic_screwdriver import Sonic, Program, Machine, InstalledProgram

# Where DentaSoft is on the agent's Windows machine. Paths are resolved there, not here.
# By default: DentaSoft.exe from the release zip, in the demo folder beside SonicScrew-Local-Agent.exe
# (the agent expands %SONIC_AGENT_DIR% to its own folder). DENTASOFT_EXECUTABLE points
# elsewhere. To run the demo from a source checkout instead, set DENTASOFT_SCRIPT to
# test-target\dentasoft\dentasoft.py (and DENTASOFT_PYTHON to the python.exe to run it with).
_script = os.getenv("DENTASOFT_SCRIPT")
if _script:
    _dentasoft = InstalledProgram(
        executable=os.getenv("DENTASOFT_PYTHON", getattr(sys, "_base_executable", sys.executable)),
        args=[_script],
        working_directory=ntpath.dirname(_script),
        window_title_contains="DentaSoft",
    )
else:
    _dentasoft = InstalledProgram(
        executable=os.getenv("DENTASOFT_EXECUTABLE", r"%SONIC_AGENT_DIR%\demo\DentaSoft.exe"),
        window_title_contains="DentaSoft",
    )
front_desk = Machine(
    name=os.getenv("DENTASOFT_MACHINE", "demo-machine"),
    programs={"dentasoft": _dentasoft},
)

# This app's project on the compute-box. SONIC_URL and SONIC_API_KEY set the connection.
dental_clinic = Sonic(
    project="demo-project",
    machine=front_desk.name,
    programs={"dentasoft": Program(description="Patient records, appointments and balances.")},
    context=(
        "Use DentaSoft Patient Manager; open the dentasoft program if needed. If its login box is showing, log in "
        "with user name drsmith and password molar123. DentaSoft is slow: after each action "
        "wait for 'Please wait...' to clear. Its text boxes only support typing and Backspace: shortcuts like Ctrl+A, Home or Delete do nothing."
    ),
)

PatientId = Annotated[str, Field(pattern=r"^P-\d{4}$", description="DentaSoft patient ID, e.g. P-1004")]


class Patient(BaseModel):
    found: bool
    first_name: str | None = None
    last_name: str | None = None
    date_of_birth: str | None = Field(default=None, description="MM/DD/YYYY as shown")
    phone: str | None = None
    insurance: str | None = None
    balance: float | None = Field(default=None, description="Balance Due in dollars")
    next_appointment: str | None = Field(default=None, description="MM/DD/YYYY, empty if none")
    allergies: str | None = None


class BalanceChange(BaseModel):
    previous_balance: float
    new_balance: float


@dental_clinic.operation
async def lookup_patient(patient_id: PatientId) -> Patient:
    """Look up a patient's record by patient ID.

    Type the patient ID into the Patient ID box in the Find Patient panel and press Find.
    Click the matching row under Results, then read the fields in the Patient Record panel,
    zooming in if any text is hard to read. If DentaSoft says no records were found,
    return found=false and leave the other fields empty.
    """


@dental_clinic.operation
async def update_balance(patient_id: PatientId, balance: Annotated[float, Field(ge=0)]) -> BalanceChange:
    """Set a patient's Balance Due.

    Find the patient by Patient ID and open their record. Note the current Balance Due.
    Click Edit, clear the Balance Due field, type the new amount with two decimals, click
    Save, answer Yes to the confirmation and OK to the success message. Then re-read
    Balance Due and only report success if it shows the new amount. If the patient does
    not exist, fail with code not_found.
    """
