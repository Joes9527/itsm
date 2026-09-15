"""Entity plug-in registry. Importing a module here is what makes it addressable from a profile."""
from migration.checks.departments import DepartmentsCheck

REGISTRY = {'departments': DepartmentsCheck()}
