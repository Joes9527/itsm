"""把新 ITSM 自带的 seed / 测试对象与新迁入数据分开。

只做分类，不连库、不写库。判定结果是"隔离清单"的输入，不是删除授权：
对象一旦被历史单据引用，就只能打标隔离，不能物理删除。
"""

import re

# 产品种子部门：由产品初始化写入，不属于集团真实组织树。
PRODUCT_SEED_DEPARTMENT_IDS = frozenset(range(1, 15))

# 非 end_user 角色账号：运维/测试/自动化用途，不属于真实员工。
# 真实员工若被改成这些角色，同样会被识别出来——这正是需要人工复核的污染。
NON_END_USER_ROLES = frozenset(
    {
        "super_admin",
        "dept_manager",
        "network_eng",
        "it_director",
        "l1_support",
        "ops_manager",
        "ops_engineer",
        "kaf_automation",
        "guest",
        "sysadmin",
    }
)

# 测试审批组：单成员脚手架，用于早期验证审批链路。
TEST_GROUP_NAMES = frozenset({"ticket-approvers", "dept_manager", "network_eng"})

# 员工工号形态：真实员工账号用 D + 数字。命中该形态的账号即使带着测试角色，
# 也是"角色被污染的真人"，不是可丢弃的 seed 账号。
EMPLOYEE_CODE_PATTERN = re.compile(r"^D\d+$")


def classify_seed_objects(rows):
    """按对象类型分出待隔离的 seed / 测试对象。

    rows: {"departments": [...], "users": [...], "groups": [...]}
    返回各类型的标识列表，均已排序，便于稳定比对与归档。

    只返回"可隔离"对象；真人账号即使角色异常也**不会**出现在这里，
    而是单列在 ``polluted_employee_roles`` 供人工纠正角色。
    """
    departments = sorted(
        d["id"] for d in rows.get("departments", []) if d["id"] in PRODUCT_SEED_DEPARTMENT_IDS
    )

    users, polluted_employee_roles = [], []
    for u in rows.get("users", []):
        if u.get("role") not in NON_END_USER_ROLES:
            continue
        username = u["username"]
        if EMPLOYEE_CODE_PATTERN.match(username):
            polluted_employee_roles.append(username)
        else:
            users.append(username)

    groups = sorted(g["name"] for g in rows.get("groups", []) if g.get("name") in TEST_GROUP_NAMES)

    return {
        "departments": departments,
        "users": sorted(users),
        "groups": groups,
        "polluted_employee_roles": sorted(polluted_employee_roles),
    }
