# coding=utf-8
# *** WARNING: this file was generated by test. ***
# *** Do not edit by hand unless you're certain you know what you are doing! ***

import builtins
import pulumi
from enum import Enum

__all__ = [
    'EnumThing',
]


@pulumi.type_token("foobar::EnumThing")
class EnumThing(builtins.int, Enum):
    FOUR = 4
    SIX = 6
    EIGHT = 8
