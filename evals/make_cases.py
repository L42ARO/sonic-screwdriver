"""Generate each suite's cases.json from the target apps' seed data.

Expected values are read from the seeds, never typed by hand, so the cases stay correct
if a seed changes. Run from the repo root:  python evals/make_cases.py
"""
import json
import os

ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))


def load(rel):
    with open(os.path.join(ROOT, rel), encoding="utf-8") as f:
        return json.load(f)


def write(suite, cases):
    path = os.path.join(ROOT, "evals", "suites", suite, "cases.json")
    with open(path, "w", encoding="utf-8") as f:
        json.dump(cases, f, indent=2)
    print(f"{suite}: {len(cases)} cases -> {os.path.relpath(path, ROOT)}")


# --------------------------------------------------------------------------- DentaSoft
patients = {p["id"]: p for p in load("test-target/dentasoft/patients.seed.json")}


def patient_record(pid):
    p = patients[pid]
    return {"found": True, "first_name": p["first"], "last_name": p["last"], "date_of_birth": p["dob"],
            "phone": p["phone"], "insurance": p["insurance"], "policy": p["policy"], "balance": p["balance"],
            "last_visit": p["last_visit"], "next_appointment": p["next_appt"], "allergies": p["allergies"]}


def by_last(prefix):
    return [{"patient_id": p["id"], "first_name": p["first"], "last_name": p["last"]}
            for p in patients.values() if p["last"].lower().startswith(prefix.lower())]


def patient_check(pid, **fields):
    return {"list": "", "where": {"id": pid}, "fields": fields}


dentasoft = [
    {"name": "lookup P-1004", "operation": "lookup_patient", "input": {"patient_id": "P-1004"},
     "expect": {"status": "succeeded", "output": patient_record("P-1004")}},
    {"name": "lookup P-1010 (notes, allergies)", "operation": "lookup_patient", "input": {"patient_id": "P-1010"},
     "expect": {"status": "succeeded", "output": patient_record("P-1010")}},
    {"name": "lookup P-1012", "operation": "lookup_patient", "input": {"patient_id": "P-1012"},
     "expect": {"status": "succeeded", "output": patient_record("P-1012")}},
    {"name": "lookup missing P-9999", "operation": "lookup_patient", "input": {"patient_id": "P-9999"},
     "expect": {"status": "succeeded", "output": {"found": False}}},
    {"name": "find last name 'Daw' (2 matches)", "operation": "find_patients_by_last_name", "input": {"last_name": "Daw"},
     "expect": {"status": "succeeded", "output": {"patients": by_last("Daw")}}},
    {"name": "find last name 'Nakamura' (1 match)", "operation": "find_patients_by_last_name", "input": {"last_name": "Nakamura"},
     "expect": {"status": "succeeded", "output": {"patients": by_last("Nakamura")}}},
    {"name": "find last name 'Zeller' (none)", "operation": "find_patients_by_last_name", "input": {"last_name": "Zeller"},
     "expect": {"status": "succeeded", "output": {"patients": []}}},
    {"name": "update balance P-1002 to 0", "operation": "update_balance", "input": {"patient_id": "P-1002", "balance": 0},
     "expect": {"status": "succeeded", "output": {"previous_balance": patients["P-1002"]["balance"], "new_balance": 0}},
     "data": [patient_check("P-1002", balance=0, phone=patients["P-1002"]["phone"])]},
    {"name": "update balance of missing P-9999", "operation": "update_balance", "input": {"patient_id": "P-9999", "balance": 10},
     "expect": {"status": "failed", "error_code": "not_found"}},
    {"name": "update phone + allergies P-1007", "operation": "update_contact",
     "input": {"patient_id": "P-1007", "phone": "(305) 555-0100", "allergies": "Peanuts"},
     "expect": {"status": "succeeded", "output": {"phone": "(305) 555-0100", "allergies": "Peanuts"}},
     "data": [patient_check("P-1007", phone="(305) 555-0100", allergies="Peanuts",
                            insurance=patients["P-1007"]["insurance"], balance=patients["P-1007"]["balance"])]},
    {"name": "set next appointment P-1002", "operation": "set_next_appointment",
     "input": {"patient_id": "P-1002", "date": "01/15/2027"},
     "expect": {"status": "succeeded", "output": {"previous": patients["P-1002"]["next_appt"], "next_appointment": "01/15/2027"}},
     "data": [patient_check("P-1002", next_appt="01/15/2027", balance=patients["P-1002"]["balance"])]},
    {"name": "about box via Help menu", "operation": "get_about_info", "input": {},
     "expect": {"status": "succeeded", "output": {"version": "2.3", "build": "1147"}}},
]
write("dentasoft", dentasoft)

# --------------------------------------------------------------------------- StockPilot
products = {p["sku"]: p for p in load("test-target/stockpilot/stockpilot.seed.json")["products"]}


def product_details(sku):
    p = products[sku]
    return {"found": True, "name": p["name"], "category": p["category"], "on_hand": p["on_hand"],
            "reorder_at": p["reorder_at"], "location": p["location"], "price": p["price"]}


def matching(q):
    q = q.lower()
    return [{"sku": p["sku"], "name": p["name"], "on_hand": p["on_hand"]}
            for p in products.values() if q in p["sku"].lower() or q in p["name"].lower()]


def low(category=None):
    return [{"sku": p["sku"], "name": p["name"], "on_hand": p["on_hand"], "reorder_at": p["reorder_at"]}
            for p in products.values()
            if p["on_hand"] <= p["reorder_at"] and (category is None or p["category"] == category)]


def priciest(category):
    p = max((p for p in products.values() if p["category"] == category), key=lambda p: p["price"])
    return {"sku": p["sku"], "name": p["name"], "price": p["price"]}


def count(category):
    return sum(p["category"] == category for p in products.values())


def product_check(sku, **fields):
    return {"list": "products", "where": {"sku": sku}, "fields": fields}


tool = products["TOO-1001"]
stockpilot = [
    {"name": "get FAS-1008", "operation": "get_product", "input": {"sku": "FAS-1008"},
     "expect": {"status": "succeeded", "output": product_details("FAS-1008")}},
    {"name": "get PNT-1036 (later page)", "operation": "get_product", "input": {"sku": "PNT-1036"},
     "expect": {"status": "succeeded", "output": product_details("PNT-1036")}},
    {"name": "get missing XYZ-9999", "operation": "get_product", "input": {"sku": "XYZ-9999"},
     "expect": {"status": "succeeded", "output": {"found": False}}},
    {"name": "find 'hex'", "operation": "find_products", "input": {"query": "hex"},
     "expect": {"status": "succeeded", "output": {"products": matching("hex")}}},
    {"name": "find 'screw'", "operation": "find_products", "input": {"query": "screw"},
     "expect": {"status": "succeeded", "output": {"products": matching("screw")}}},
    {"name": "low stock, all categories", "operation": "list_low_stock", "input": {},
     "expect": {"status": "succeeded", "output": {"products": low()}}},
    {"name": "low stock, Fasteners", "operation": "list_low_stock", "input": {"category": "Fasteners"},
     "expect": {"status": "succeeded", "output": {"products": low("Fasteners")}}},
    {"name": "count Plumbing", "operation": "count_products", "input": {"category": "Plumbing"},
     "expect": {"status": "succeeded", "output": {"count": count("Plumbing")}}},
    {"name": "most expensive in Tools", "operation": "most_expensive_product", "input": {"category": "Tools"},
     "expect": {"status": "succeeded", "output": priciest("Tools")}},
    {"name": "receive 20 x TOO-1001", "operation": "adjust_stock",
     "input": {"sku": "TOO-1001", "change": 20, "reason": "Received", "note": "PO 4471"},
     "expect": {"status": "succeeded", "output": {"previous_on_hand": tool["on_hand"], "new_on_hand": tool["on_hand"] + 20}},
     "data": [product_check("TOO-1001", on_hand=tool["on_hand"] + 20),
              {"list": "movements", "where": {"sku": "TOO-1001"}, "fields": {"change": 20, "reason": "Received", "note": "PO 4471"}}]},
    {"name": "sell 5 x ELE-1015 (only 0 on hand)", "operation": "adjust_stock",
     "input": {"sku": "ELE-1015", "change": -5, "reason": "Sold"},
     "expect": {"status": "failed", "error_code": "invalid_input"}},
    {"name": "create ELE-2001", "operation": "create_product",
     "input": {"sku": "ELE-2001", "name": "LED Bulb 9W", "category": "Electrical", "reorder_at": 12, "location": "B-02-3", "price": 3.49},
     "expect": {"status": "succeeded", "output": {"created": True, "sku": "ELE-2001"}},
     "data": [product_check("ELE-2001", name="LED Bulb 9W", category="Electrical", reorder_at=12, location="B-02-3", price=3.49, on_hand=0)]},
    {"name": "create duplicate FAS-1008", "operation": "create_product",
     "input": {"sku": "FAS-1008", "name": "Duplicate", "category": "Fasteners", "reorder_at": 5, "location": "A-01-1", "price": 1},
     "expect": {"status": "failed", "error_code": "invalid_input"}},
]
write("stockpilot", stockpilot)
