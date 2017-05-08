class PipelineTemplate < ArvadosModel
  include HasUuid
  include KindAndEtag
  include CommonApiTemplate
  serialize :components, Hash

  api_accessible :user, extend: :common do |t|
    t.add :name
    t.add :components
    t.add :description
  end

  def self.limit_index_columns_read
    ["components"]
  end
end
