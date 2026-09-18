from scripts.migration.seed_isolation_plan import classify_seed_objects


def test_classifies_product_seed_departments_and_test_accounts():
    rows = {
        "departments": [
            {"id": 1, "code": "IT", "name": "信息技术部"},
            {"id": 15, "code": "1", "name": "组织架构"},
        ],
        "users": [
            {"username": "admin", "role": "super_admin", "active": True},
            {"username": "qa_manager_ops", "role": "ops_manager", "active": True},
            {"username": "D12345", "role": "end_user", "active": True},
        ],
        "groups": [{"name": "ticket-approvers"}, {"name": "dept_manager"}],
    }
    plan = classify_seed_objects(rows)
    assert plan["departments"] == [1]
    assert plan["users"] == ["admin", "qa_manager_ops"]
    assert plan["groups"] == ["dept_manager", "ticket-approvers"]


def test_end_users_are_never_classified_as_seed():
    plan = classify_seed_objects(
        {
            "departments": [],
            "users": [{"username": "D1", "role": "end_user", "active": True}],
            "groups": [],
        }
    )
    assert plan["users"] == []


def test_real_employees_with_test_roles_are_flagged_not_disposed():
    """真实员工（工号形态 D#####）被改成测试角色时必须单列，绝不能进待隔离清单。"""
    plan = classify_seed_objects(
        {
            "departments": [],
            "groups": [],
            "users": [
                {"username": "D31717", "role": "dept_manager", "active": True},
                {"username": "D18998", "role": "network_eng", "active": True},
                {"username": "qa_manager_ops", "role": "ops_manager", "active": True},
                {"username": "supervisor_test", "role": "dept_manager", "active": True},
            ],
        }
    )
    assert plan["users"] == ["qa_manager_ops", "supervisor_test"]
    assert plan["polluted_employee_roles"] == ["D18998", "D31717"]
