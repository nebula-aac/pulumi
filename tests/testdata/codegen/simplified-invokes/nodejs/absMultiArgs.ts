// *** WARNING: this file was generated by test. ***
// *** Do not edit by hand unless you're certain you know what you are doing! ***

import * as pulumi from "@pulumi/pulumi";
import * as utilities from "./utilities";

/**
 * Returns the absolute value of a given float.
 * Example: abs(1) returns 1, and abs(-1) would also return 1, whereas abs(-3.14) would return 3.14.
 */
export function absMultiArgs(a: number, b?: number, opts?: pulumi.InvokeOptions): Promise<AbsMultiArgsResult> {
    opts = pulumi.mergeOptions(utilities.resourceOptsDefaults(), opts || {});
    return pulumi.runtime.invoke("std:index:AbsMultiArgs", {
        "a": a,
        "b": b,
    }, opts);
}

export interface AbsMultiArgsResult {
    readonly result: number;
}
/**
 * Returns the absolute value of a given float.
 * Example: abs(1) returns 1, and abs(-1) would also return 1, whereas abs(-3.14) would return 3.14.
 */
export function absMultiArgsOutput(a: pulumi.Input<number>, b?: pulumi.Input<number | undefined>, opts?: pulumi.InvokeOutputOptions): pulumi.Output<AbsMultiArgsResult> {
    opts = pulumi.mergeOptions(utilities.resourceOptsDefaults(), opts || {});
    return pulumi.runtime.invokeOutput("std:index:AbsMultiArgs", {
        "a": a,
        "b": b,
    }, opts);
}
