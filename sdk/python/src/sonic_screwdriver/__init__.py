"""Typed Python client for the sonic-screwdriver compute-box.

    from sonic_screwdriver import Sonic

    dentasoft = Sonic(project="dental-clinic", machine="dentist-front-desk")

    @dentasoft.operation
    async def lookup_patient(patient_id: str) -> Patient:
        \"\"\"Look up a patient's record by patient ID.

        Type the ID into the Patient ID box, press Find, open the row, read the record.
        \"\"\"

    await dentasoft.sync()                 # once, at startup
    patient = await lookup_patient("P-1004")
"""
from sonic_screwdriver.client import Sonic, Step, Machine
from sonic_screwdriver.programs import Program, InstalledProgram
from sonic_screwdriver.errors import SonicError
from sonic_screwdriver.operation import Operation

__all__ = ["Sonic", "Operation", "Step", "SonicError", "Machine", "Program", "InstalledProgram"]
