// *** WARNING: this file was generated by test. ***
// *** Do not edit by hand unless you're certain you know what you are doing! ***

using System;
using System.Collections.Generic;
using System.Collections.Immutable;
using System.Threading.Tasks;
using Pulumi.Serialization;

namespace Pulumi.Mypkg
{
    public static class FuncWithDictParam
    {
        /// <summary>
        /// Check codegen of functions with a Dict&lt;str,str&gt; parameter.
        /// </summary>
        public static Task<FuncWithDictParamResult> InvokeAsync(FuncWithDictParamArgs? args = null, InvokeOptions? options = null)
            => global::Pulumi.Deployment.Instance.InvokeAsync<FuncWithDictParamResult>("mypkg::funcWithDictParam", args ?? new FuncWithDictParamArgs(), options.WithDefaults());

        /// <summary>
        /// Check codegen of functions with a Dict&lt;str,str&gt; parameter.
        /// </summary>
        public static Output<FuncWithDictParamResult> Invoke(FuncWithDictParamInvokeArgs? args = null, InvokeOptions? options = null)
            => global::Pulumi.Deployment.Instance.Invoke<FuncWithDictParamResult>("mypkg::funcWithDictParam", args ?? new FuncWithDictParamInvokeArgs(), options.WithDefaults());

        /// <summary>
        /// Check codegen of functions with a Dict&lt;str,str&gt; parameter.
        /// </summary>
        public static Output<FuncWithDictParamResult> Invoke(FuncWithDictParamInvokeArgs args, InvokeOutputOptions options)
            => global::Pulumi.Deployment.Instance.Invoke<FuncWithDictParamResult>("mypkg::funcWithDictParam", args ?? new FuncWithDictParamInvokeArgs(), options.WithDefaults());
    }


    public sealed class FuncWithDictParamArgs : global::Pulumi.InvokeArgs
    {
        [Input("a")]
        private Dictionary<string, string>? _a;
        public Dictionary<string, string> A
        {
            get => _a ?? (_a = new Dictionary<string, string>());
            set => _a = value;
        }

        [Input("b")]
        public string? B { get; set; }

        public FuncWithDictParamArgs()
        {
        }
        public static new FuncWithDictParamArgs Empty => new FuncWithDictParamArgs();
    }

    public sealed class FuncWithDictParamInvokeArgs : global::Pulumi.InvokeArgs
    {
        [Input("a")]
        private InputMap<string>? _a;
        public InputMap<string> A
        {
            get => _a ?? (_a = new InputMap<string>());
            set => _a = value;
        }

        [Input("b")]
        public Input<string>? B { get; set; }

        public FuncWithDictParamInvokeArgs()
        {
        }
        public static new FuncWithDictParamInvokeArgs Empty => new FuncWithDictParamInvokeArgs();
    }


    [OutputType]
    public sealed class FuncWithDictParamResult
    {
        public readonly string R;

        [OutputConstructor]
        private FuncWithDictParamResult(string r)
        {
            R = r;
        }
    }
}
