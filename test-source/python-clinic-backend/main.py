"""Clinic patient-portal backend. Its patient data lives in DentaSoft, reached via the agent.

    uvicorn main:app --reload
"""
import logging

from fastapi import FastAPI

from integrations import sonic
from routes import patients

logging.basicConfig(level=logging.INFO, format="%(asctime)s %(name)s %(message)s")

app = FastAPI(title="Clinic Patient Portal", lifespan=sonic.lifespan)
sonic.setup(app)
app.include_router(patients.router)
