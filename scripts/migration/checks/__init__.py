"""Entity plug-in registry. Importing a module here is what makes it addressable from a profile."""
from .departments import DepartmentsCheck
from .users import UsersCheck

REGISTRY = {'departments': DepartmentsCheck(), 'users': UsersCheck()}
