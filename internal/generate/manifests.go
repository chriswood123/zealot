package generate

// Minimal Kubernetes manifest types — only the fields zealot emits, in
// marshaling order (yaml.v3 emits struct fields in declaration order).

type metadata struct {
	Name      string            `yaml:"name,omitempty"`
	Namespace string            `yaml:"namespace,omitempty"`
	Labels    map[string]string `yaml:"labels,omitempty"`
}

type namespaceDoc struct {
	APIVersion string   `yaml:"apiVersion"`
	Kind       string   `yaml:"kind"`
	Metadata   metadata `yaml:"metadata"`
}

type configMapDoc struct {
	APIVersion string            `yaml:"apiVersion"`
	Kind       string            `yaml:"kind"`
	Metadata   metadata          `yaml:"metadata"`
	Data       map[string]string `yaml:"data"`
}

type persistentVolumeClaimDoc struct {
	APIVersion string   `yaml:"apiVersion"`
	Kind       string   `yaml:"kind"`
	Metadata   metadata `yaml:"metadata"`
	Spec       pvcSpec  `yaml:"spec"`
}

type pvcSpec struct {
	AccessModes []string     `yaml:"accessModes"`
	Resources   pvcResources `yaml:"resources"`
}

type pvcResources struct {
	Requests map[string]string `yaml:"requests"`
}

type deploymentDoc struct {
	APIVersion string         `yaml:"apiVersion"`
	Kind       string         `yaml:"kind"`
	Metadata   metadata       `yaml:"metadata"`
	Spec       deploymentSpec `yaml:"spec"`
}

type deploymentSpec struct {
	Replicas int         `yaml:"replicas"`
	Selector selector    `yaml:"selector"`
	Template podTemplate `yaml:"template"`
}

type selector struct {
	MatchLabels map[string]string `yaml:"matchLabels"`
}

type podTemplate struct {
	Metadata metadata `yaml:"metadata"`
	Spec     podSpec  `yaml:"spec"`
}

type podSpec struct {
	Containers []container `yaml:"containers"`
	Volumes    []volume    `yaml:"volumes,omitempty"`
}

type container struct {
	Name            string                `yaml:"name"`
	Image           string                `yaml:"image"`
	ImagePullPolicy string                `yaml:"imagePullPolicy,omitempty"`
	Args            []string              `yaml:"args,omitempty"`
	Env             []envVar              `yaml:"env,omitempty"`
	Ports           []containerPort       `yaml:"ports,omitempty"`
	Resources       *resourceRequirements `yaml:"resources,omitempty"`
	ReadinessProbe  *probe                `yaml:"readinessProbe,omitempty"`
	LivenessProbe   *probe                `yaml:"livenessProbe,omitempty"`
	VolumeMounts    []volumeMount         `yaml:"volumeMounts,omitempty"`
}

type envVar struct {
	Name  string `yaml:"name"`
	Value string `yaml:"value"`
}

type containerPort struct {
	Name          string `yaml:"name"`
	ContainerPort int    `yaml:"containerPort"`
}

type resourceRequirements struct {
	Requests map[string]string `yaml:"requests,omitempty"`
	Limits   map[string]string `yaml:"limits,omitempty"`
}

type probe struct {
	HTTPGet *httpGet   `yaml:"httpGet,omitempty"`
	Exec    *execProbe `yaml:"exec,omitempty"`
}

type httpGet struct {
	Path string `yaml:"path"`
	Port string `yaml:"port"`
}

type execProbe struct {
	Command []string `yaml:"command"`
}

type volumeMount struct {
	Name      string `yaml:"name"`
	MountPath string `yaml:"mountPath"`
	ReadOnly  bool   `yaml:"readOnly,omitempty"`
}

type volume struct {
	Name      string        `yaml:"name"`
	ConfigMap *configMapVol `yaml:"configMap,omitempty"`
	PVC       *pvcVol       `yaml:"persistentVolumeClaim,omitempty"`
}

type configMapVol struct {
	Name string `yaml:"name"`
}

type pvcVol struct {
	ClaimName string `yaml:"claimName"`
}

type serviceDoc struct {
	APIVersion string      `yaml:"apiVersion"`
	Kind       string      `yaml:"kind"`
	Metadata   metadata    `yaml:"metadata"`
	Spec       serviceSpec `yaml:"spec"`
}

type serviceSpec struct {
	Type     string            `yaml:"type,omitempty"`
	Selector map[string]string `yaml:"selector"`
	Ports    []servicePort     `yaml:"ports"`
}

type servicePort struct {
	Name       string `yaml:"name"`
	Port       int    `yaml:"port"`
	TargetPort string `yaml:"targetPort"`
	NodePort   int    `yaml:"nodePort,omitempty"`
}

type serviceMonitorDoc struct {
	APIVersion string             `yaml:"apiVersion"`
	Kind       string             `yaml:"kind"`
	Metadata   metadata           `yaml:"metadata"`
	Spec       serviceMonitorSpec `yaml:"spec"`
}

type serviceMonitorSpec struct {
	Selector  selector          `yaml:"selector"`
	Endpoints []monitorEndpoint `yaml:"endpoints"`
}

type monitorEndpoint struct {
	Port string `yaml:"port"`
}
